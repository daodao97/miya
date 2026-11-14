package nschat

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/chat/subagent"
	"miya/internal/nswidgets"
	"miya/internal/textutil"
)

// subAgentList shows the sub-agents an activity tracks, whichever agent's
// adapter reported them: each one's state, name and latest step. Rows are
// kept by the sub-agent's ID, so a running one updates in place.
func subAgentList(cx *core.Scope, agents func() []model.SubAgent) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{TestID: "subagents",
		StyleFunc: func() core.Style {
			return core.Style{Gap: 8, Padding: core.All(12), Background: colors.Subtle(), CornerRadius: 8, MinWidth: core.Pt(0)}
		},
		Children: []core.Node{core.Each(agents, func(a model.SubAgent) string { return a.ID },
			func(cx *core.Scope, a func() model.SubAgent, _ func() int) core.Node { return subAgentRow(cx, a) })},
	}
}

func subAgentRow(cx *core.Scope, a func() model.SubAgent) core.Node {
	colors := nswidgets.ColorsOf(cx)
	id := core.Untracked(cx, func() string { return a().ID })
	running := core.NewMemo(cx, func() bool { return a().Status == subagent.Running })
	tint := func() core.Color { return markerColor(colors, a().Status) }
	return core.View{TestID: "subagent-" + id, Style: core.Style{Direction: core.Row, Gap: 8, AlignItems: core.AlignStart, MinWidth: core.Pt(0)}, Children: []core.Node{
		// Markers and the spinner share one width, so names line up.
		core.View{Style: core.Style{Width: core.Pt(16), Height: core.Pt(16), FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{core.Show{When: running.Get,
				Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 12} },
				Else: func(*core.Scope) core.Node {
					return core.Text{ContentFunc: func() string { return marker(a().Status) },
						StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: tint()} }}
				}}}},
		core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), Gap: 2}, Children: []core.Node{
			core.Text{MaxLines: 1, ContentFunc: func() string { s := a(); return msgfmt.NativeText(textutil.FirstNonEmpty(s.Name, s.ID)) },
				StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: colors.Ink()} }},
			core.Text{Selectable: core.SelectableOn, ContentFunc: func() string { return msgfmt.NativeText(subAgentLine(a())) },
				StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: tint()} }},
		}},
	}}
}

// subAgentLine is a sub-agent's state, steps and latest step.
func subAgentLine(a model.SubAgent) string {
	line := subagent.StatusLabel(a.Status)
	if a.Steps > 0 {
		line += fmt.Sprintf(tr(" · %d steps"), a.Steps)
	}
	if a.Detail != "" {
		line += " · " + a.Detail
	}
	return line
}

func marker(status string) string {
	switch status {
	case subagent.Completed:
		return "✓"
	case subagent.Failed:
		return "✕"
	case subagent.Running:
		return "◉"
	case subagent.Interrupted:
		return "■"
	}
	return "○"
}

func markerColor(colors nswidgets.Colors, status string) core.Color {
	switch status {
	case subagent.Completed:
		return colors.Success()
	case subagent.Failed:
		return colors.Failed()
	case subagent.Running:
		return colors.Ink()
	}
	return colors.Muted()
}
