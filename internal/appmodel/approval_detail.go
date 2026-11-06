package appmodel

import (
	"strings"

	"miya/internal/codex"
	"miya/internal/model"
	"miya/internal/msgfmt"
)

func ApprovalDetail(threads []model.Thread, a codex.Approval) string {
	text := a.Reason + "\n" + a.Command
	if a.Cwd != "" {
		text += "\nDirectory: " + a.Cwd
	}
	if a.GrantRoot != "" {
		text += "\nRequested root: " + a.GrantRoot
	}
	if len(a.Permissions) > 0 {
		text += "\n" + msgfmt.JsonText(a.Permissions)
	}
	for _, t := range threads {
		if t.ServerID == a.ThreadID {
			text = "Thread: " + t.Title + "\n" + text
			if a.Method == "item/fileChange/requestApproval" {
				for _, m := range t.Messages {
					if m.ItemID == a.ItemID {
						text += "\n" + m.Content
					}
				}
			}
		}
	}
	return strings.TrimSpace(text)
}
