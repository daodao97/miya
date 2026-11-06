package main

import (
	"fmt"
	"strings"

	"miya/internal/textutil"

	"github.com/egoist/mygo/ui"
)

// groupCache keeps the presentation groups of the open conversation until
// the controller changes it, so a frame does not regroup its history.
type groupCache struct {
	thread, revision, count int
	busy                    bool
	turn                    string
	completedAt             int64
	groups                  []messageGroup
}

func (g *groupCache) of(c conversationHost) []messageGroup {
	return g.ofThread(c.Current(), c.Revision())
}

func (g *groupCache) ofThread(t Thread, revision int) []messageGroup {
	if g.groups != nil && g.thread == t.ID && g.revision == revision && g.count == len(t.Messages) && g.busy == t.Busy && g.turn == t.TurnID && g.completedAt == t.CompletedAtMs {
		return g.groups
	}
	*g = groupCache{thread: t.ID, revision: revision, count: len(t.Messages), busy: t.Busy, turn: t.TurnID, completedAt: t.CompletedAtMs}
	g.groups = threadMessageGroups(t, groupMessages(t.Messages))
	if g.groups == nil {
		g.groups = []messageGroup{}
	}
	return g.groups
}

func (app *chatApp) chatView(c *ui.Context) {
	cur := app.c.current()
	if cur.ID == 0 {
		app.home(c)
	} else {
		app.messageList(c, cur)
	}
	app.composerArea(c)
}

func (app *chatApp) home(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(100).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Padding(0, 0, 40, 0).Children(func() {
		icon(c, "brand-openai", 46, ink)
		ui.Text(c, tr("Let’s build")).FontSize(28).FontWeight(600)
		pick := quiet(c.Key("pick-project")).Height(36)
		pick.Children(func() {
			ui.Text(c, app.project()).FontSize(26).TextColor(muted)
			icon(c, "chevron-down", 16, muted)
		})
		if pick.Clicked() {
			app.openProjectDialog()
		}
	})
}

// messageList shows the open conversation. Each conversation keeps its own
// list state, so its place and following of the end survive switching.

// Keep the current turn above the composer in both states. Older responses
// retain their historical timing; the latest completed response uses this row.
func currentTurnStatus(t Thread, groups []messageGroup) (string, int) {
	if t.Busy {
		return threadWorkingLabel(t), -1
	}
	if last := len(groups) - 1; last >= 0 && !groups[last].User {
		return turnElapsedLabel(groups[last].Messages, false), last
	}
	return "", -1
}

func responseDiffStats(c *ui.Context, stats DiffLineStats) {
	if stats.Added == 0 && stats.Deleted == 0 {
		return
	}
	ui.Row(c).Shrink(0).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("+%d", stats.Added)).TextColor(success).FontSize(11)
		ui.Text(c, fmt.Sprintf("−%d", stats.Deleted)).TextColor(danger).FontSize(11)
	})
}

// messageView shows one protocol item; activity is not assistant prose.

func activityTitle(m Message) string {
	active := m.Status == "inProgress" || m.Status == "in_progress"
	switch strings.ToLower(m.Kind) {
	case "claudetool":
		return claudeToolTitle(m)
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
	return messageLabel(m.Kind)
}

// activityBody is an activity's own detail: its plan text, diff or output.

func diffView(c *ui.Context, diff string) {
	ui.Column(c).FillWidth().MinWidth(0).Background(codeBg).Radius(8).Padding(10).Selectable().Children(func() {
		for i, l := range strings.Split(textutil.Bounded(diff), "\n") {
			color := muted
			switch {
			case strings.HasPrefix(l, "+"):
				color = added
			case strings.HasPrefix(l, "-"):
				color = failed
			case strings.HasPrefix(l, "@@"):
				color = hunk
			}
			ui.Text(c.Key(i), l).Font(monoFont).FontSize(12).LineHeight(1.5).TextColor(color)
		}
	})
}

// listPadding spaces the conversation; the phone is narrower.
