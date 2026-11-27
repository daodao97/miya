package presentation

import (
	"fmt"
	"strings"

	"miya/internal/chat/model"
	chattext "miya/internal/chat/text"
)

type ActivitySummary struct {
	Label     string
	DiffStats model.DiffLineStats
	Running   bool
}

// SummarizeActivities describes one consecutive run of activities. The turn
// diff can include edits from other runs, so only count patches on the items.
func SummarizeActivities(messages []model.Message) ActivitySummary {
	edited, read := map[string]struct{}{}, map[string]struct{}{}
	var searches, listings, commands, other, running, failed, interrupted int
	var summary ActivitySummary
	for _, m := range messages {
		kind := strings.ToLower(m.Kind)
		if kind == "diff" {
			continue
		}
		switch m.Status {
		case "inProgress", "in_progress", "running":
			if len(m.Agents) == 0 {
				running++
				continue
			}
			// The activity header already displays tracked sub-agent states.
		case "failed":
			failed++
			continue
		case "interrupted":
			interrupted++
			continue
		}
		switch kind {
		case "filechange":
			changes := Message(m).Changes
			if len(changes) == 0 {
				other++
			}
			for _, change := range changes {
				if change.Path == "" {
					other++
				} else {
					edited[change.Path] = struct{}{}
				}
				if m.Status == "completed" {
					stats := model.CountDiffLines(change.Diff)
					summary.DiffStats.Added += stats.Added
					summary.DiffStats.Deleted += stats.Deleted
				}
			}
		case "commandexecution":
			if m.Item == nil || len(m.Item.CommandActions) == 0 {
				commands++
				continue
			}
			var ranCommand bool
			for _, action := range m.Item.CommandActions {
				switch action.Type {
				case "read":
					path := action.Path
					if path == "" {
						path = action.Name
					}
					if path != "" {
						read[path] = struct{}{}
					} else {
						ranCommand = true
					}
				case "search":
					searches++
				case "listFiles":
					listings++
				default:
					ranCommand = true
				}
			}
			if ranCommand {
				commands++
			}
		case "websearch":
			searches++
		case "claudetool":
			name, input := chattext.ToolCall(m)
			switch name {
			case "Bash":
				commands++
			case "Read", "NotebookRead":
				if path := chattext.InputString(input, "file_path", "notebook_path", "path"); path != "" {
					read[path] = struct{}{}
				} else {
					other++
				}
			case "Edit", "MultiEdit", "Write", "NotebookEdit":
				if path := chattext.InputString(input, "file_path", "notebook_path", "path"); path != "" {
					edited[path] = struct{}{}
				} else {
					other++
				}
			case "Grep", "WebSearch":
				searches++
			case "Glob", "LS":
				listings++
			default:
				other++
			}
		default:
			other++
		}
	}

	var parts []string
	add := func(n int, singular, plural string) {
		if n == 1 {
			parts = append(parts, singular)
		} else if n > 1 {
			parts = append(parts, fmt.Sprintf(plural, n))
		}
	}
	add(len(edited), tr("Edited 1 file"), tr("Edited %d files"))
	add(len(read), tr("Read 1 file"), tr("Read %d files"))
	add(searches, tr("1 search"), tr("%d searches"))
	add(listings, tr("1 file listing"), tr("%d file listings"))
	add(commands, tr("Ran 1 command"), tr("Ran %d commands"))
	if len(parts) == 0 {
		add(other, tr("1 activity"), tr("%d activities"))
	} else {
		add(other, tr("1 other activity"), tr("%d other activities"))
	}
	add(running, tr("1 running"), tr("%d running"))
	add(failed, tr("1 failed"), tr("%d failed"))
	add(interrupted, tr("1 interrupted"), tr("%d interrupted"))
	if len(parts) == 0 {
		add(len(messages), tr("1 activity"), tr("%d activities"))
	}
	summary.Label = strings.Join(parts, " · ")
	summary.Running = running > 0
	return summary
}
