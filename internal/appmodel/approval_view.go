package appmodel

import (
	"encoding/json"
	"fmt"
	"strings"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/interaction"
)

// ApprovalView is how a card presents an approval: what approving does, in
// a short phrase, then exactly what the agent would act on. Cards on the
// desktop and the phone draw the same parts, each in its own style.
type ApprovalView struct {
	// Icon is a Tabler icon name for the kind of action.
	Icon string
	// Title says what approving does, as "Run a command".
	Title string
	// Summary is the agent's own explanation, when it gave one.
	Summary string
	// Code is the command, path, address or input verbatim, for a
	// monospaced block that can be read and copied exactly.
	Code string
	// Changes are proposed edits, as the conversation shows them.
	Changes string
	// Facts are labelled details, such as the working directory.
	Facts []ApprovalFact
	// Thread is the title of the conversation that asked.
	Thread string
}

type ApprovalFact struct{ Label, Value string }

// maxChanges bounds the edits a card shows; the conversation has them all.
const maxChanges = 4000

func DescribeApproval(threads []model.Thread, a interaction.Request) ApprovalView {
	v := ApprovalView{Icon: "shield-exclamation", Title: tr("Approve this action"), Summary: a.Reason, Code: a.Command}
	switch a.Method {
	case "item/commandExecution/requestApproval":
		v.Icon, v.Title = "terminal-2", tr("Run a command")
	case "item/fileChange/requestApproval":
		v.Icon, v.Title = "file-diff", tr("Change files")
	case "item/permissions/requestApproval":
		v.Icon, v.Title = "lock-open", tr("Grant more access")
	case "miya/desktopControl":
		v.Icon, v.Title = "alert-circle", tr("Needs your attention")
	case "claude/permission":
		v = describeClaudeTool(a)
	}
	if v.Code == "" {
		v.Code = msgfmt.JsonText(a.Permissions)
	}
	if a.Cwd != "" {
		v.Facts = append(v.Facts, ApprovalFact{tr("Directory"), a.Cwd})
	}
	if a.GrantRoot != "" {
		v.Facts = append(v.Facts, ApprovalFact{tr("Requested root"), a.GrantRoot})
	}
	for _, t := range threads {
		if a.ThreadID == "" || t.ServerID != a.ThreadID {
			continue
		}
		v.Thread = t.Title
		if a.Method == "item/fileChange/requestApproval" {
			for _, m := range t.Messages {
				if m.ItemID == a.ItemID {
					v.Changes = strings.TrimSpace(v.Changes + "\n" + m.Content)
				}
			}
		}
		break
	}
	v.Summary, v.Code = strings.TrimSpace(v.Summary), strings.TrimSpace(v.Code)
	v.Changes = bounded(v.Changes, maxChanges)
	return v
}

// describeClaudeTool reads Claude Code's tool input, so the card names the
// action and shows the one value it acts on rather than raw JSON.
func describeClaudeTool(a interaction.Request) ApprovalView {
	raw := a.Payload
	if len(raw) == 0 {
		raw = json.RawMessage(a.Reason)
	}
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	str := func(key string) string {
		s, _ := in[key].(string)
		return strings.TrimSpace(s)
	}
	v := ApprovalView{Icon: "shield-exclamation"}
	switch a.Command {
	case "Bash":
		v.Icon, v.Title, v.Code, v.Summary = "terminal-2", tr("Run a command"), str("command"), str("description")
	case "Edit", "MultiEdit":
		v.Icon, v.Title, v.Code = "file-diff", tr("Edit a file"), str("file_path")
		v.Changes = editChanges(in)
	case "Write":
		v.Icon, v.Title, v.Code = "file-diff", tr("Write a file"), str("file_path")
		v.Changes = str("content")
	case "NotebookEdit":
		v.Icon, v.Title, v.Code = "file-diff", tr("Edit a notebook"), str("notebook_path")
	case "Read":
		v.Icon, v.Title, v.Code = "file-text", tr("Read a file"), str("file_path")
	case "Glob", "Grep":
		v.Icon, v.Title, v.Code = "folder", tr("Search files"), str("pattern")
		if path := str("path"); path != "" {
			v.Facts = append(v.Facts, ApprovalFact{tr("Directory"), path})
		}
	case "WebFetch":
		v.Icon, v.Title, v.Code, v.Summary = "file-text", tr("Open a web page"), str("url"), str("prompt")
	case "WebSearch":
		v.Icon, v.Title, v.Code = "file-text", tr("Search the web"), str("query")
	}
	if v.Title == "" {
		name := a.Command
		if server, tool, ok := mcpTool(name); ok {
			name = tool
			v.Facts = append(v.Facts, ApprovalFact{tr("Server"), server})
		}
		v.Title = tr("Use a tool")
		if name != "" {
			v.Title = fmt.Sprintf(tr("Use %s"), name)
		}
	}
	if v.Code == "" {
		if text := msgfmt.JsonText(raw); text != "{}" {
			v.Code = text
		}
	}
	return v
}

// mcpTool splits Claude Code's mcp__server__tool names.
func mcpTool(name string) (server, tool string, ok bool) {
	rest, found := strings.CutPrefix(name, "mcp__")
	if !found {
		return "", "", false
	}
	server, tool, ok = strings.Cut(rest, "__")
	return server, tool, ok && server != "" && tool != ""
}

// editChanges shows an edit's replaced and new text as removed and added
// lines, the way a review reads them.
func editChanges(in map[string]any) string {
	edits := []any{in}
	if list, ok := in["edits"].([]any); ok {
		edits = list
	}
	var b strings.Builder
	for _, e := range edits {
		edit, _ := e.(map[string]any)
		removed, _ := edit["old_string"].(string)
		added, _ := edit["new_string"].(string)
		if removed == "" && added == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		if removed != "" {
			for _, line := range strings.Split(removed, "\n") {
				b.WriteString("- " + line + "\n")
			}
		}
		if added != "" {
			for _, line := range strings.Split(added, "\n") {
				b.WriteString("+ " + line + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func bounded(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "\n…"
}
