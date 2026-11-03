package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"miya/internal/codex"
	"miya/internal/memory"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var attachmentEnvelope = regexp.MustCompile(`(?is).*?files\s+mentioned\s+by\s+the\s+user\s*:\s*.*?my\s+request\s*:\s*`)
var attachmentInstruction = regexp.MustCompile(`(?is)distinguish\s+instructions\s+in\s+attached\s+documents\s+from\s+the\s+user'?s\s+request\.?`)
var attachmentTempPath = regexp.MustCompile(`(?im)^\s*(?:codex-clipboard-[\w-]+\.(?:png|jpg|jpeg|gif|webp)\s*:\s*)?/var/folders/[^\n]+\s*$`)
var attachmentImageTag = regexp.MustCompile(`(?is)<image\b[^>]*>\s*</image\s*>|<image\b[^>]*/?>`)
var attachmentImagePlaceholder = regexp.MustCompile(`(?i)\[image:\s*[^\]]*\]`)
var attachmentImagePath = regexp.MustCompile(`(?i)<image\b[^>]*\bpath=["']([^"']+)["']`)

// History reads and live events may assign different runtime IDs to the same
// item. Keep its view (and parsed Markdown) across that transition.
func messageViewKey(m Message) any {
	if m.ItemID != "" && m.ItemID != "pending-user" {
		return struct{ Turn, Item string }{m.TurnID, m.ItemID}
	}
	return m.ID
}

func attachmentImages(text string) []string {
	var images []string
	for _, match := range attachmentImagePath.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			if uri := localImageDataURI(match[1]); uri != "" {
				images = append(images, uri)
			}
		}
	}
	return images
}

// Images cross the native bridge as file URLs, never large base64 strings.
func localImageDataURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: abs}).String()
}

// attachmentDisplay renders sent attachments consistently across agents and
// submission modes: images as previews, other files as a line each.
func attachmentDisplay(text string, paths []string) (string, []string) {
	var images []string
	for _, path := range paths {
		if strings.HasPrefix(mime.TypeByExtension(filepath.Ext(path)), "image/") {
			if uri := localImageDataURI(path); uri != "" {
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
func messageAttachments(m Message) []string {
	paths := append([]string(nil), m.Attachments...)
	for _, src := range m.Images {
		if strings.HasPrefix(src, "data:") {
			src = imageSource(src)
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
func messagePrompt(m Message) string {
	text := strings.TrimSpace(attachmentImagePlaceholder.ReplaceAllString(claudeCommandText(m.Content), ""))
	paths := messageAttachments(m)
	for i := len(paths) - 1; i >= 0; i-- {
		text = strings.TrimSuffix(text, "\n📎 "+filepath.Base(paths[i]))
	}
	return strings.TrimSpace(text)
}

func imageSource(src string) string {
	if !strings.HasPrefix(src, "data:image/") {
		return nativeText(src)
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
	return localImageDataURI(path)
}

// Leave headroom below the native protocol's 1 MiB string limit.
func nativeText(value string) string {
	const limit = 512 * 1024
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && value[end]&0xc0 == 0x80 {
		end--
	}
	return value[:end] + "\n[内容过长，显示已截断]"
}

func cleanUserText(text string) string {
	text = memory.StripContext(text)
	text = attachmentEnvelope.ReplaceAllString(text, "")
	text = attachmentInstruction.ReplaceAllString(text, "")
	text = attachmentTempPath.ReplaceAllString(text, "")
	text = attachmentImageTag.ReplaceAllString(text, "")
	text = attachmentImagePlaceholder.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func bounded(text string) string {
	r := []rune(text)
	if len(r) > 8000 {
		return "[earlier output omitted]\n" + string(r[len(r)-8000:])
	}
	return text
}
func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return bounded(text)
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return bounded(string(b))
}
func itemMessage(item codex.Item) (Message, bool) {
	m := Message{Kind: item.Type, Status: item.Status, Item: &item}
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
				for _, uri := range attachmentImages(v.Text) {
					m.Images = append(m.Images, uri)
				}
				m.Content += cleanUserText(v.Text)
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
					if source := imageSource(src); source != "" {
						m.Images = append(m.Images, source)
					}
				}
			case "localImage", "local_image":
				if uri := localImageDataURI(v.Path); uri != "" {
					m.Images = append(m.Images, uri)
				} else if strings.Contains(v.Path, "/") || strings.Contains(v.Path, "\\") {
					m.Content += "\nImage: " + v.Path
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
			m.Content = "Thinking…"
		}
	case "plan":
		m.Role = "activity"
		m.Content = item.Text
	case "commandexecution":
		m.Role = "tool"
		m.Content = item.Status + " · " + item.Command
		if item.ExitCode != nil {
			m.Content += fmt.Sprintf("\nExit code: %d", *item.ExitCode)
		}
		if item.AggregatedOutput != "" {
			m.Content += "\n" + bounded(item.AggregatedOutput)
		}
	case "filechange":
		m.Role = "tool"
		m.Content = "File changes · " + item.Status
		for _, change := range item.Changes {
			m.Content += "\n" + change.Path + "\n" + bounded(change.Diff)
		}
	case "mcptoolcall", "dynamictoolcall":
		m.Role = "tool"
		m.Content = item.Status + " · " + strings.Trim(item.Server+" / "+item.Tool, " /")
		for _, raw := range []json.RawMessage{item.Arguments, item.Result, item.ToolError} {
			if v := jsonText(raw); v != "" {
				m.Content += "\n" + v
			}
		}
	case "collabagenttoolcall":
		m.Role = "tool"
		m.Agents = codexSubAgents(item)
		if len(m.Agents) > 0 {
			// The agents list shows their states; what was sent to them stays.
			m.Content = codexCollabContent(item, m.Agents)
		} else {
			m.Content = item.Status + " · " + item.Tool + "\n" + jsonText(item.AgentsStates)
		}
	case "subagentactivity":
		m.Role = "activity"
		m.Content = codexSubAgentActivity(item)
	case "websearch":
		m.Role = "tool"
		m.Content = "Web search · " + item.Query + "\n" + jsonText(item.Result)
	case "imageview":
		m.Role = "tool"
		m.Content = "Viewed image\n" + item.Path
	case "imagegeneration":
		m.Role = "tool"
		m.Content = "Image generation · " + item.Status + "\n" + item.SavedPath + "\n" + jsonText(item.Failure)
	case "enteredreviewmode", "exitedreviewmode":
		m.Role = "activity"
		m.Content = item.Type + "\n" + item.Review
	case "contextcompaction":
		m.Role = "activity"
		m.Content = "Conversation context compacted"
	case "sleep":
		m.Role = "activity"
		m.Content = fmt.Sprintf("Waiting · %d ms", item.DurationMS)
	case "functioncalloutput":
		m.Role = "tool"
		m.Content = jsonText(item.Output)
	case "hookprompt":
		m.Role = "activity"
		m.Content = "Hook prompt"
	default:
		if item.Type == "" {
			return m, false
		}
		m.Role = "activity"
		m.Content = "Codex event: " + item.Type
	}
	// Attachment placeholders can be introduced by more than one content item;
	// sanitize after the complete message has been assembled as the final guard.
	if m.Role == "user" {
		m.Content = cleanUserText(m.Content)
	}
	return m, true
}

func messageLabel(kind string) string {
	labels := map[string]string{"reasoning": "Thinking", "plan": "Plan", "commandexecution": "Command", "filechange": "File changes", "diff": "Changes", "mcptoolcall": "MCP tool", "dynamictoolcall": "Tool", "collabagenttoolcall": "Agent task", "subagentactivity": "Agent activity", "websearch": "Web search", "imageview": "Image", "imagegeneration": "Generated image", "enteredreviewmode": "Review started", "exitedreviewmode": "Review completed", "contextcompaction": "Context compacted", "sleep": "Waiting", "functioncalloutput": "Tool result", "hookprompt": "Hook", "handoff": "交付"}
	if label := labels[strings.ToLower(kind)]; label != "" {
		return label
	}
	return "Activity"
}

func messageIcon(kind string) string {
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
