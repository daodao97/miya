// Package msgfmt turns conversation messages into what the desktop and the
// phone show: grouped activity, labels, attachments, timings and token rates.
package presentation

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"miya/internal/chat/model"
	chattext "miya/internal/chat/text"
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
			if uri := chattext.LocalImageURI(match[1]); uri != "" {
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
			if uri := chattext.LocalImageURI(path); uri != "" {
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
	text := strings.TrimSpace(model.ImagePlaceholder.ReplaceAllString(chattext.CommandText(m.Content), ""))
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
	return chattext.LocalImageURI(path)
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
func ResetTurnOutput(t *model.Thread) {
	t.TurnOutputTokens = nil
	t.TurnOutputBaseline = nil
	t.ClaudeOutputTokens = nil
	t.ClaudeMessageID = ""
	if t.Usage != nil && t.Usage.Total.OutputTokens != nil {
		t.TurnOutputBaseline = new(*t.Usage.Total.OutputTokens)
	}
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
	return chattext.ClippedTitle(textutil.FirstNonEmpty(chattext.ClippedTitle(name), chattext.ClippedTitle(preview), chattext.ClippedTitle(existing), "Untitled thread"))
}

// activityIcon is a Claude tool's own icon, else its kind's.
func ActivityIcon(m model.Message) string {
	if !strings.EqualFold(m.Kind, "claudeTool") {
		return MessageIcon(m.Kind)
	}
	name, _ := chattext.ToolCall(m)
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
