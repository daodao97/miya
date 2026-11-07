package claude

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentpolicy "miya/internal/agent/policy"
	"miya/internal/appdir"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/locales"
)

// claudeErrorMessage explains an empty result, which Claude Code reports as
// a diagnostic such as "[ede_diagnostic] … stop_reason=null" when the
// request got no answer, keeping its own words below.
func ErrorMessage(message string, hadOutput, customCommand bool) string {
	if hadOutput && !strings.Contains(message, "ede_diagnostic") {
		return message
	}
	hint := "Claude Code returned no response. Check your connection and sign-in status, then try again."
	if customCommand {
		hint = "Claude Code returned no response. Check proxy or API settings in the custom launch command under Settings → General, or clear the command and try again."
	}
	return hint + "\n\nOriginal error: " + message
}

func HistoryPath(id string) string {
	if id == "" || filepath.Base(id) != id {
		return ""
	}
	dir := appdir.File("quickgui-claude")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id+".json")
}

func Input(text string, paths []string) ([]map[string]any, error) {
	blocks := []map[string]any{{"type": "text", "text": text}}
	for _, p := range paths {
		typ := mime.TypeByExtension(filepath.Ext(p))
		if strings.HasPrefix(typ, "image/") {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			if len(data) > 20*1024*1024 {
				return nil, fmt.Errorf("Image is too large: %s", filepath.Base(p))
			}
			blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": typ, "data": base64.StdEncoding.EncodeToString(data)}})
		} else {
			blocks = append(blocks, map[string]any{"type": "text", "text": "Attached file: " + p})
		}
	}
	return blocks, nil
}

// claudePermissionMode maps the app's permission choices to Claude Code's
// modes, so one picker serves both agents.
func PermissionMode(policy string) string {
	switch agentpolicy.Normalize(policy) {
	case "never":
		return "bypassPermissions"
	case "on-failure":
		return "auto"
	default:
		return "manual"
	}
}

// claudeStallAfter is how long a run may send nothing before the app says
// it is still waiting.
const StallAfter = 60 * time.Second

// claudeTextKey is the message a complete text block of a reply fills in.
// Claude Code sends each block of a reply in its own assistant frame, where
// it is at index 0, while streamed deltas carry the block's index in the
// whole reply; so the block completes the streamed message of its text, and
// an unstreamed block takes the first key no other text holds.
func TextKey(t *model.Thread, messageID string, index int, text string) string {
	return blockKey(t, messageID+":", index, text, "assistant")
}

// Thinking blocks use a separate namespace because complete assistant frames
// carry one block at index zero, while stream events use the original index.
func ThinkingKey(t *model.Thread, messageID string, index int, text string) string {
	return blockKey(t, messageID+":thinking:", index, text, "activity")
}

func blockKey(t *model.Thread, prefix string, index int, text, role string) string {
	partial := ""
	used := map[string]bool{}
	for _, m := range t.Messages {
		if m.Role != role || !strings.HasPrefix(m.ItemID, prefix) {
			continue
		}
		if m.Content == text {
			return m.ItemID
		}
		if partial == "" && (m.Content != "" || role == "activity") && strings.HasPrefix(text, m.Content) {
			partial = m.ItemID
		}
		used[m.ItemID] = true
	}
	if partial != "" {
		return partial
	}
	for k := index; ; k++ {
		if key := fmt.Sprintf("%s%d", prefix, k); !used[key] {
			return key
		}
	}
}

func SaveHistory(t model.Thread) {
	p := HistoryPath(t.ServerID)
	if p == "" {
		return
	}
	data, err := json.Marshal(t)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".history-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() == nil {
		_ = os.Rename(name, p)
	}
}

// turnHasOutput reports whether the turn since the last prompt produced a
// reply or a tool call.
func TurnHasOutput(messages []model.Message) bool {
	return msgfmt.TurnHasOutput(messages)
}

func ModelName(v string) string {
	if n, ok := claudeModelNames[v]; ok {
		return n
	}
	return v
}

// claudeModelNames are the names of Claude's models and aliases.
var claudeModelNames = map[string]string{
	"claude-fable-5-1":  "Claude Fable 5.1",
	"claude-opus-5-5":   "Claude Opus 5.5",
	"claude-sonnet-5-5": "Claude Sonnet 5.5",
	"claude-haiku-5-5":  "Claude Haiku 5.5",
	"fable":             tr("Fable (latest)"),
	"opus":              tr("Opus (latest)"),
	"sonnet":            tr("Sonnet (latest)"),
	"haiku":             tr("Haiku (latest)"),
}

func tr(s string) string { return locales.T(s) }
