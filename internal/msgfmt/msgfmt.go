// Package msgfmt turns conversation messages into what the desktop and the
// phone show: grouped activity, labels, attachments, timings and token rates.
package msgfmt

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"miya/internal/agent/claude/native/transcript"
	"miya/internal/agent/codex/native"
	"miya/internal/memory"
	"miya/internal/model"
	"miya/internal/subagent"
	"miya/internal/textutil"
	"miya/locales"
)

var attachmentImagePath = regexp.MustCompile(`(?i)<image\b[^>]*\bpath=["']([^"']+)["']`)

// History reads and live events may assign different runtime IDs to the same
// item. Keep its view (and parsed Markdown) across that transition.
func MessageViewKey(m model.Message) any {
	if m.ItemID != "" && m.ItemID != "pending-user" {
		return struct{ Turn, Item string }{m.TurnID, m.ItemID}
	}
	return m.ID
}

func AttachmentImages(text string) []string {
	var images []string
	for _, match := range attachmentImagePath.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			if uri := transcript.LocalImageURI(match[1]); uri != "" {
				images = append(images, uri)
			}
		}
	}
	return images
}

// attachmentDisplay renders sent attachments consistently across agents and
// submission modes: images as previews, other files as a line each.
func AttachmentDisplay(text string, paths []string) (string, []string) {
	var images []string
	for _, path := range paths {
		if strings.HasPrefix(mime.TypeByExtension(filepath.Ext(path)), "image/") {
			if uri := transcript.LocalImageURI(path); uri != "" {
				images = append(images, uri)
				continue
			}
		}
		text += "\n📎 " + filepath.Base(path)
	}
	return text, images
}

// Keep the actual attachment paths for a resend; older transcripts may only
// retain image URLs, which need to become local paths again.
func MessageAttachments(m model.Message) []string {
	paths := append([]string(nil), m.Attachments...)
	for _, src := range m.Images {
		if strings.HasPrefix(src, "data:") {
			src = ImageSource(src)
		}
		if strings.HasPrefix(src, "file://") {
			u, err := url.Parse(src)
			if err != nil {
				continue
			}
			src = u.Path
		}
		if !filepath.IsAbs(src) {
			continue
		}
		found := false
		for _, path := range paths {
			found = found || path == src
		}
		if !found {
			paths = append(paths, src)
		}
	}
	return paths
}

// Remove only attachment display suffixes we can resend as real attachments.
// Keep the original reference tokens and their send-time context snapshots.
func MessagePrompt(m model.Message) string {
	if m.Presentation != nil {
		m.Content = m.Presentation.Content
	}
	text := strings.TrimSpace(model.ImagePlaceholder.ReplaceAllString(transcript.CommandText(m.Content), ""))
	paths := MessageAttachments(m)
	for i := len(paths) - 1; i >= 0; i-- {
		text = strings.TrimSuffix(text, "\n📎 "+filepath.Base(paths[i]))
	}
	return strings.TrimSpace(text)
}

func ImageSource(src string) string {
	if !strings.HasPrefix(src, "data:image/") {
		return NativeText(src)
	}
	header, payload, ok := strings.Cut(src, ",")
	if !ok {
		return ""
	}
	var data []byte
	var err error
	if strings.HasSuffix(header, ";base64") {
		data, err = base64.StdEncoding.DecodeString(payload)
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil {
		return ""
	}
	dir := filepath.Join(os.TempDir(), "quickgui-image-cache")
	if os.MkdirAll(dir, 0700) != nil {
		return ""
	}
	mimeType := strings.Split(strings.TrimPrefix(header, "data:"), ";")[0]
	ext := ".img"
	if extensions, _ := mime.ExtensionsByType(mimeType); len(extensions) > 0 {
		ext = extensions[0]
	}
	path := filepath.Join(dir, fmt.Sprintf("%x%s", sha256.Sum256(data), ext))
	if _, err := os.Stat(path); err != nil {
		if os.WriteFile(path, data, 0600) != nil {
			return ""
		}
	}
	return transcript.LocalImageURI(path)
}

// Leave headroom below the native protocol's 1 MiB string limit.
func NativeText(value string) string {
	const limit = 512 * 1024
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && value[end]&0xc0 == 0x80 {
		end--
	}
	return value[:end] + tr("\n[Content truncated for display]")
}

func JsonText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return textutil.Bounded(text)
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return textutil.Bounded(string(b))
}

func ItemMessage(item codex.Item) (model.Message, bool) {
	m := model.Message{Kind: item.Type, Status: item.Status, Item: &item}
	switch strings.ToLower(item.Type) {
	case "agentmessage":
		m.Role = "assistant"
		m.Content = item.Text
	case "usermessage":
		m.Role = "user"
		var parts []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Path     string `json:"path"`
			URL      string `json:"url"`
			ImageURL string `json:"image_url"`
			Name     string `json:"name"`
			Data     string `json:"data"`
			MIMEType string `json:"mime_type"`
		}
		_ = json.Unmarshal(item.Content, &parts)
		for _, v := range parts {
			switch v.Type {
			case "text":
				v.Text = memory.StripContext(v.Text)
				for _, uri := range AttachmentImages(v.Text) {
					m.Images = append(m.Images, uri)
				}
				m.Content += model.CleanUserText(v.Text)
			case "image", "input_image":
				src := strings.TrimSpace(v.URL)
				if src == "" {
					src = strings.TrimSpace(v.ImageURL)
				}
				if src == "" {
					src = strings.TrimSpace(v.Data)
					if src != "" && !strings.HasPrefix(src, "data:") {
						mimeType := v.MIMEType
						if mimeType == "" {
							mimeType = "image/png"
						}
						src = "data:" + mimeType + ";base64," + src
					}
				}
				if src != "" {
					if source := ImageSource(src); source != "" {
						m.Images = append(m.Images, source)
					}
				}
			case "localImage", "local_image":
				if uri := transcript.LocalImageURI(v.Path); uri != "" {
					m.Images = append(m.Images, uri)
				} else if strings.Contains(v.Path, "/") || strings.Contains(v.Path, "\\") {
					m.Content += tr("\nImage: ") + v.Path
				}
			case "skill", "mention":
				m.Content += "\n[" + v.Type + ": " + v.Name + " " + v.Path + "]"
			default:
				m.Content += "\n[" + v.Type + "]"
			}
		}
	case "reasoning":
		m.Role = "activity"
		m.Content = strings.Join(item.Summary, "\n")
		if m.Content == "" {
			m.Content = tr("Thinking…")
		}
	case "plan":
		m.Role = "activity"
		m.Content = item.Text
	case "commandexecution":
		m.Role = "tool"
		m.Content = item.Status + " · " + item.Command
		if item.ExitCode != nil {
			m.Content += fmt.Sprintf(tr("\nExit code: %d"), *item.ExitCode)
		}
		if item.AggregatedOutput != "" {
			m.Content += "\n" + textutil.Bounded(item.AggregatedOutput)
		}
	case "filechange":
		m.Role = "tool"
		m.Content = tr("File changes · ") + item.Status
		for _, change := range item.Changes {
			m.Content += "\n" + change.Path + "\n" + textutil.Bounded(change.Diff)
		}
	case "mcptoolcall", "dynamictoolcall":
		m.Role = "tool"
		m.Content = item.Status + " · " + strings.Trim(item.Server+" / "+item.Tool, " /")
		for _, raw := range []json.RawMessage{item.Arguments, item.Result, item.ToolError} {
			if v := JsonText(raw); v != "" {
				m.Content += "\n" + v
			}
		}
	case "collabagenttoolcall":
		m.Role = "tool"
		m.Agents = subagent.FromCodex(item)
		if len(m.Agents) > 0 {
			// The agents list shows their states; what was sent to them stays.
			m.Content = subagent.CodexCollabContent(item, m.Agents)
		} else {
			m.Content = item.Status + " · " + item.Tool + "\n" + JsonText(item.AgentsStates)
		}
	case "subagentactivity":
		m.Role = "activity"
		m.Content = subagent.CodexActivity(item)
	case "websearch":
		m.Role = "tool"
		m.Content = tr("Web search · ") + item.Query + "\n" + JsonText(item.Result)
	case "imageview":
		m.Role = "tool"
		m.Content = tr("Viewed image\n") + item.Path
	case "imagegeneration":
		m.Role = "tool"
		m.Content = tr("Image generation · ") + item.Status + "\n" + item.SavedPath + "\n" + JsonText(item.Failure)
	case "enteredreviewmode", "exitedreviewmode":
		m.Role = "activity"
		m.Content = item.Type + "\n" + item.Review
	case "contextcompaction":
		m.Role = "activity"
		m.Content = tr("Conversation context compacted")
	case "sleep":
		m.Role = "activity"
		m.Content = fmt.Sprintf(tr("Waiting · %d ms"), item.DurationMS)
	case "functioncalloutput":
		m.Role = "tool"
		m.Content = JsonText(item.Output)
	case "hookprompt":
		m.Role = "activity"
		m.Content = tr("Hook prompt")
	default:
		if item.Type == "" {
			return m, false
		}
		m.Role = "activity"
		m.Content = tr("Codex event: ") + item.Type
	}
	// Attachment placeholders can be introduced by more than one content item;
	// sanitize after the complete message has been assembled as the final guard.
	if m.Role == "user" {
		m.Content = model.CleanUserText(m.Content)
	}
	return m, true
}

func MessageIcon(kind string) string {
	switch strings.ToLower(kind) {
	case "commandexecution", "mcptoolcall", "dynamictoolcall", "functioncalloutput":
		return "terminal-2"
	case "filechange", "diff", "imageview", "imagegeneration":
		return "file-text"
	case "reasoning", "sleep":
		return "clock"
	case "plan", "collabagenttoolcall", "subagentactivity", "handoff":
		return "stack-2"
	default:
		return "message-circle"
	}
}

type MessageGroup struct {
	ID            int
	User          bool
	TurnID        string
	Messages      []model.Message
	Streaming     bool
	Timestamp     int64
	ShowTimestamp bool
	DiffStats     model.DiffLineStats
}

const CollapsedThreadCount = 5

// displayedThreads keeps a busy project sidebar compact while preserving the
// newest conversations. The full list remains owned by the controller.
func DisplayedThreads(all []model.Thread, expanded bool) []model.Thread {
	all = append([]model.Thread(nil), all...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].LastActivityAt > all[j].LastActivityAt })
	if expanded || len(all) <= CollapsedThreadCount {
		return all
	}
	return all[:CollapsedThreadCount]
}

func ThreadMessageGroups(t model.Thread, groups []MessageGroup) []MessageGroup {
	if !t.Busy {
		// Older services recorded completion only on the thread. Recover its
		// latest response without mutating the controller's message slice.
		if len(groups) > 0 && t.StartedAtMs > 0 && t.CompletedAtMs >= t.StartedAtMs {
			g := &groups[len(groups)-1]
			if !g.User {
				g.Messages = append([]model.Message(nil), g.Messages...)
				for i := range g.Messages {
					if g.Messages[i].CompletedAtMs == 0 {
						g.Messages[i].StartedAtMs = t.StartedAtMs
						g.Messages[i].CompletedAtMs = t.CompletedAtMs
					}
				}
			}
		}
		return groups
	}
	for i := range groups {
		if groups[i].User {
			continue
		}
		if t.TurnID == "" {
			groups[i].Streaming = i == len(groups)-1
			continue
		}
		for _, m := range groups[i].Messages {
			if m.TurnID == t.TurnID {
				groups[i].Streaming = true
				break
			}
		}
	}
	return groups
}

// Group protocol items for presentation without merging or modifying their data.
// Tool activity belongs to the same response; a user message or new turn separates it.
func GroupMessages(messages []model.Message) []MessageGroup {
	var groups []MessageGroup
	for _, m := range messages {
		// Old services appended automatic memory cards without an explicit flag.
		// Keep those historical records out of both desktop and phone timelines.
		if m.Role == "memory" && (m.Memory == nil || !m.Memory.Explicit) {
			continue
		}
		// Reasoning stays in the thread model, but is not a visible timeline item.
		// Filter before grouping so it creates neither empty responses nor counts.
		if strings.EqualFold(m.Kind, "reasoning") {
			continue
		}
		user := m.Role == "user"
		newGroup := len(groups) == 0 || user
		if !newGroup {
			last := groups[len(groups)-1]
			// The app-server may emit assistant and activity items with different
			// item/turn metadata while one response is still being assembled. Keep
			// those pieces in one response group; only two consecutive assistant
			// messages with different turns start a new response.
			lastRole := ""
			if len(last.Messages) > 0 {
				lastRole = last.Messages[len(last.Messages)-1].Role
			}
			newGroup = last.User || (m.Role == "assistant" && lastRole == "assistant" && m.TurnID != "" && last.TurnID != "" && m.TurnID != last.TurnID)
		}
		if newGroup {
			groups = append(groups, MessageGroup{ID: m.ID, User: user, TurnID: m.TurnID})
		}
		g := &groups[len(groups)-1]
		if g.TurnID == "" {
			g.TurnID = m.TurnID
		}
		g.Messages = append(g.Messages, m)
	}
	for i := range groups {
		if !groups[i].User {
			groups[i].DiffStats = model.TurnDiffStats(groups[i].Messages)
		}
		for _, m := range groups[i].Messages {
			if m.SentAtMs > 0 {
				groups[i].Timestamp = m.SentAtMs
				break
			}
		}
		if groups[i].Timestamp == 0 {
			continue
		}
		if i == 0 || groups[i-1].Timestamp == 0 || groups[i].Timestamp-groups[i-1].Timestamp >= 10*60*1000 {
			groups[i].ShowTimestamp = true
		}
	}
	return groups
}

func MessageGroupText(g MessageGroup) string {
	var parts []string
	for _, m := range g.Messages {
		if m.Presentation != nil {
			m.Content = m.Presentation.Content
		}
		if m.Content != "" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func FormatMessageTimestamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).In(time.Local)
	now := time.Now().In(time.Local)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("3:04 PM")
	}
	if now.After(t) && now.Sub(t) < 7*24*time.Hour {
		return t.Format("Monday 3:04 PM")
	}
	return t.Format("Jan 2, 2006 3:04 PM")
}

func TurnElapsedLabel(messages []model.Message, active bool) string {
	var start, end int64
	for _, m := range messages {
		if m.StartedAtMs > 0 && (start == 0 || m.StartedAtMs < start) {
			start = m.StartedAtMs
		}
		if m.CompletedAtMs > end {
			end = m.CompletedAtMs
		}
	}
	if start == 0 {
		return ""
	}
	if end == 0 {
		if !active {
			return ""
		}
		end = time.Now().UnixMilli()
	}
	verb := tr("Worked for ")
	if active {
		verb = tr("Working for ")
	}
	return verb + FormatElapsed(end-start)
}

func FormatElapsed(ms int64) string {
	if ms < 1000 {
		return "under 1s"
	}
	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if minutes < 60 {
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
}

func ThreadWorkingLabel(t model.Thread) string {
	if label := ThreadReconnectLabel(t); label != "" {
		return label
	}
	if t.StartedAtMs > 0 {
		elapsed := time.Now().UnixMilli() - t.StartedAtMs
		label := tr("Working for ") + FormatElapsed(elapsed)
		if rate := ThreadTokenRateLabel(t, elapsed); rate != "" {
			label += " · " + rate
		}
		return label
	}
	if t.Status != "" {
		return tr(t.Status)
	}
	return tr("Waiting for agent…")
}

var reconnectAttempt = regexp.MustCompile(`\b\d+\s*/\s*\d+\b`)

// Retry status is also understood by older desktop and phone versions: it
// travels in the existing thread metadata, including updates with no text.
func ThreadReconnectLabel(t model.Thread) string {
	if !t.Busy || !strings.HasPrefix(t.Status, "Retrying: ") {
		return ""
	}
	label := tr("Reconnecting…")
	if attempt := reconnectAttempt.FindString(t.Status); attempt != "" {
		label += " " + strings.ReplaceAll(attempt, " ", "")
	}
	return label
}

// Group only consecutive activities; commentary and answers retain their order.
type ActivityRun struct {
	ID       int
	Activity bool
	Messages []model.Message
}

func ActivityRuns(messages []model.Message) []ActivityRun {
	var runs []ActivityRun
	for _, m := range messages {
		activity := m.Role == "tool" || m.Role == "activity"
		if !activity || len(runs) == 0 || !runs[len(runs)-1].Activity {
			runs = append(runs, ActivityRun{ID: m.ID, Activity: activity})
		}
		runs[len(runs)-1].Messages = append(runs[len(runs)-1].Messages, m)
	}
	return runs
}

// Protocol timestamps are seconds; durationMs preserves sub-second precision.
// Missing timestamps in older rollouts remain unknown rather than using now.
func CodexTurnTimes(turn codex.Turn) (start, end int64) {
	if turn.StartedAt > 0 {
		start = turn.StartedAt * 1000
	}
	if turn.Status == "inProgress" {
		return start, 0
	}
	if turn.CompletedAt > 0 {
		end = turn.CompletedAt * 1000
	}
	if turn.DurationMS != nil && *turn.DurationMS >= 0 {
		if start > 0 {
			end = start + *turn.DurationMS
		} else if end > *turn.DurationMS {
			start = end - *turn.DurationMS
		}
	}
	return start, end
}

// Claude's transport does not attach a turn ID to its response items.
func CompleteClaudeTiming(t *model.Thread) {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		m := &t.Messages[i]
		if m.Role == "user" {
			break
		}
		if t.StartedAtMs > 0 {
			m.StartedAtMs = t.StartedAtMs
		}
		m.CompletedAtMs = t.CompletedAtMs
	}
}

// Keep a turn's output separate from context/input tokens and the totals of
// earlier turns. Only the reported count travels to windows and phones.
func ResetTurnOutput(t *model.Thread) {
	t.TurnOutputTokens = nil
	t.TurnOutputBaseline = nil
	t.ClaudeOutputTokens = nil
	t.ClaudeMessageID = ""
	if t.Usage != nil && t.Usage.Total.OutputTokens != nil {
		t.TurnOutputBaseline = new(*t.Usage.Total.OutputTokens)
	}
}

func RecordCodexOutput(t *model.Thread, usage *codex.TokenUsage) {
	if !t.Busy || usage == nil || usage.Total.OutputTokens == nil {
		return
	}
	total := *usage.Total.OutputTokens
	if t.TurnOutputBaseline == nil {
		// A resumed session may first report its historical total during this
		// turn. The last request identifies which output is new.
		last := usage.Last.OutputTokens
		if last == nil || *last < 0 || *last > total {
			return
		}
		t.TurnOutputBaseline = new(total - *last)
	}
	if total < *t.TurnOutputBaseline {
		// A reset/compacted total cannot be compared with the old baseline.
		t.TurnOutputTokens = nil
		return
	}
	t.TurnOutputTokens = new(total - *t.TurnOutputBaseline)
}

func RecordClaudeOutput(t *model.Thread, messageID string, raw json.RawMessage) {
	if !t.Busy || messageID == "" {
		return
	}
	var usage struct {
		OutputTokens *int `json:"output_tokens"`
	}
	if json.Unmarshal(raw, &usage) != nil || usage.OutputTokens == nil || *usage.OutputTokens < 0 {
		return
	}
	// Claude reports cumulative usage for each message, sometimes more than
	// once. Reconcile by message ID rather than adding duplicate reports.
	counts := maps.Clone(t.ClaudeOutputTokens)
	if counts == nil {
		counts = make(map[string]int)
	}
	counts[messageID] = max(counts[messageID], *usage.OutputTokens)
	t.ClaudeOutputTokens = counts
	total := 0
	for _, count := range counts {
		total += count
	}
	t.TurnOutputTokens = new(total)
}

func ThreadTokenRateLabel(t model.Thread, elapsedMs int64) string {
	if !t.Busy || t.TurnOutputTokens == nil || elapsedMs <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f tokens/s", float64(*t.TurnOutputTokens)*1000/float64(elapsedMs))
}

// Named agent titles take precedence over a first-request preview. Retain the
// existing title when a partial server response has neither.
func ConversationTitle(name, preview, existing string) string {
	return transcript.ClippedTitle(textutil.FirstNonEmpty(transcript.ClippedTitle(name), transcript.ClippedTitle(preview), transcript.ClippedTitle(existing), "Untitled thread"))
}

// activityIcon is a Claude tool's own icon, else its kind's.
func ActivityIcon(m model.Message) string {
	if !strings.EqualFold(m.Kind, "claudeTool") {
		return MessageIcon(m.Kind)
	}
	name, _ := transcript.ToolCall(m)
	switch name {
	case "Bash":
		return "terminal-2"
	case "Read", "NotebookRead":
		return "file-text"
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "edit"
	case "Grep", "Glob", "LS":
		return "folder"
	case "WebFetch", "WebSearch":
		return "globe"
	case "Task", "Agent":
		return "stack-2"
	}
	return MessageIcon(m.Kind)
}

func tr(s string) string { return locales.T(s) }
