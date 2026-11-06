package conversation

import (
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	textutil "miya/internal/textutil"
	widgets "miya/internal/widgets"
	strings "strings"

	ui "github.com/egoist/mygo/ui"
)

// groupCache keeps the presentation groups of the open conversation until
// the controller changes it, so a frame does not regroup its history.
type GroupCache struct {
	Thread, Revision, Count int
	Busy                    bool
	turn                    string
	CompletedAt             int64
	Groups                  []appmodel.MessageGroup
}

func (g *GroupCache) Of(c Host) []appmodel.MessageGroup {
	return g.ofThread(c.ViewThread(), c.ViewRevision())
}

func (g *GroupCache) ofThread(t appmodel.Thread, revision int) []appmodel.MessageGroup {
	if g.Groups != nil && g.Thread == t.ID && g.Revision == revision && g.Count == len(t.Messages) && g.Busy == t.Busy && g.turn == t.TurnID && g.CompletedAt == t.CompletedAtMs {
		return g.Groups
	}
	*g = GroupCache{Thread: t.ID, Revision: revision, Count: len(t.Messages), Busy: t.Busy, turn: t.TurnID, CompletedAt: t.CompletedAtMs}
	g.Groups = appmodel.ThreadMessageGroups(t, appmodel.GroupMessages(t.Messages))
	if g.Groups == nil {
		g.Groups = []appmodel.MessageGroup{}
	}
	return g.Groups
}

// Keep the current turn above the composer in both states. Older responses
// retain their historical timing; the latest completed response uses this row.
func currentTurnStatus(t appmodel.Thread, groups []appmodel.MessageGroup) (string, int) {
	if t.Busy {
		return appmodel.ThreadWorkingLabel(t), -1
	}
	if last := len(groups) - 1; last >= 0 && !groups[last].User {
		return appmodel.TurnElapsedLabel(groups[last].Messages, false), last
	}
	return "", -1
}

func responseDiffStats(c *ui.Context, stats appmodel.DiffLineStats) {
	if stats.Added == 0 && stats.Deleted == 0 {
		return
	}
	ui.Row(c).Shrink(0).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("+%d", stats.Added)).TextColor(widgets.Success).FontSize(11)
		ui.Text(c, fmt.Sprintf("−%d", stats.Deleted)).TextColor(widgets.Danger).FontSize(11)
	})
}

func ActivityTitle(m appmodel.Message) string {
	active := m.Status == "inProgress" || m.Status == "in_progress"
	switch strings.ToLower(m.Kind) {
	case "claudetool":
		return appmodel.ClaudeToolTitle(m)
	case "handoff":
		if m.HandoffTitle != "" {
			return tr("Handed off to conversation \"") + m.HandoffTitle + "\""
		}
		return tr("Handed off to another conversation")
	case "reasoning":
		if active {
			return tr("Thinking…")
		}
		return tr("Thought")
	case "commandexecution":
		if m.Item != nil && len(m.Item.CommandActions) > 0 {
			a := m.Item.CommandActions[0]
			switch a.Type {
			case "read":
				return tr("Read ") + textutil.FirstNonEmpty(a.Name, a.Path, tr("Files"))
			case "search":
				return tr("Searched ") + textutil.FirstNonEmpty(a.Query, a.Path, tr("Files"))
			case "listFiles":
				return tr("Listed ") + textutil.FirstNonEmpty(a.Path, tr("Files"))
			}
		}
		verb := tr("Ran ")
		if active {
			verb = tr("Running ")
		} else if m.Status == "failed" {
			verb = tr("Failed · ")
		} else if m.Status == "interrupted" {
			verb = tr("Stopped · ")
		}
		if m.Item != nil && m.Item.Command != "" {
			return verb + strings.SplitN(m.Item.Command, "\n", 2)[0]
		}
		return verb + "command"
	case "filechange":
		verb := tr("Edited")
		if active {
			verb = tr("Editing")
		} else if m.Status == "failed" {
			verb = tr("Failed to edit")
		}
		if m.Item != nil {
			return fmt.Sprintf("%s %d files", verb, len(m.Item.Changes))
		}
		return verb + " files"
	case "plan":
		if len(m.Plan) > 0 {
			done := 0
			for _, p := range m.Plan {
				if p.Status == "completed" {
					done++
				}
			}
			return fmt.Sprintf(tr("Plan · %d / %d completed"), done, len(m.Plan))
		}
		return tr("Proposed plan")
	case "mcptoolcall", "dynamictoolcall":
		if m.Item != nil {
			name := strings.Trim(m.Item.Server+" · "+m.Item.Tool, " ·")
			if name != "" {
				if active {
					return tr("Calling ") + name
				}
				return tr("Called ") + name
			}
		}
	case "websearch":
		if m.Item != nil {
			if active {
				return tr("Searching · ") + m.Item.Query
			}
			return tr("Searched · ") + m.Item.Query
		}
	case "diff":
		return tr("Changes in this turn")
	}
	return appmodel.MessageLabel(m.Kind)
}

func diffView(c *ui.Context, diff string) {
	ui.Column(c).FillWidth().MinWidth(0).Background(widgets.CodeBg).Radius(8).Padding(10).Selectable().Children(func() {
		for i, l := range strings.Split(textutil.Bounded(diff), "\n") {
			color := widgets.Muted
			switch {
			case strings.HasPrefix(l, "+"):
				color = widgets.Added
			case strings.HasPrefix(l, "-"):
				color = widgets.Failed
			case strings.HasPrefix(l, "@@"):
				color = widgets.Hunk
			}
			ui.Text(c.Key(i), l).Font(widgets.MonoFont).FontSize(12).LineHeight(1.5).TextColor(color)
		}
	})
}
