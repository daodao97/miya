package view

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

// groupCache keeps the presentation groups of the open conversation until
// the controller changes it, so a frame does not regroup its history.
type GroupCache struct {
	Thread, Revision, Count int
	Busy                    bool
	turn                    string
	CompletedAt             int64
	Groups                  []msgfmt.MessageGroup
}

func (g *GroupCache) Of(c Host) []msgfmt.MessageGroup {
	return g.ofThread(c.ViewThread(), c.ViewRevision())
}

func (g *GroupCache) ofThread(t model.Thread, revision int) []msgfmt.MessageGroup {
	if g.Groups != nil && g.Thread == t.ID && g.Revision == revision && g.Count == len(t.Messages) && g.Busy == t.Busy && g.turn == t.TurnID && g.CompletedAt == t.CompletedAtMs {
		return g.Groups
	}
	*g = GroupCache{Thread: t.ID, Revision: revision, Count: len(t.Messages), Busy: t.Busy, turn: t.TurnID, CompletedAt: t.CompletedAtMs}
	g.Groups = msgfmt.ThreadMessageGroups(t, msgfmt.GroupMessages(t.Messages))
	if g.Groups == nil {
		g.Groups = []msgfmt.MessageGroup{}
	}
	return g.Groups
}

// Keep the current turn above the composer in both states. Older responses
// retain their historical timing; the latest completed response uses this row.
func currentTurnStatus(t model.Thread, groups []msgfmt.MessageGroup) (string, int) {
	if t.Busy {
		return msgfmt.ThreadWorkingLabel(t), -1
	}
	if last := len(groups) - 1; last >= 0 && !groups[last].User {
		return msgfmt.TurnElapsedLabel(groups[last].Messages, false), last
	}
	return "", -1
}

func responseDiffStats(c *ui.Context, stats model.DiffLineStats) {
	if stats.Added == 0 && stats.Deleted == 0 {
		return
	}
	ui.Row(c).Shrink(0).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("+%d", stats.Added)).TextColor(widgets.Success).FontSize(11)
		ui.Text(c, fmt.Sprintf("−%d", stats.Deleted)).TextColor(widgets.Danger).FontSize(11)
	})
}

func ActivityTitle(m model.Message) string { return msgfmt.Message(m).Title }

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
