package conversation

import (
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	subagent "miya/internal/subagent"
	textutil "miya/internal/textutil"
	widgets "miya/internal/widgets"

	ui "github.com/egoist/mygo/ui"
)

// subAgentList shows the sub-agents an activity tracks, whichever agent's
// adapter reported them: each one's state, name and latest step.
func subAgentList(c *ui.Context, agents []appmodel.SubAgent) {
	ui.Column(c).FillWidth().MinWidth(0).Gap(8).Padding(12).Background(widgets.Subtle).Radius(8).Children(func() {
		for i, a := range agents {
			marker, color := subAgentMarker(a.Status)
			ui.Row(c.Key(i)).Gap(8).AlignItems(ui.Start).Children(func() {
				// Markers and the spinner share one width, so names line up.
				ui.Box(c).Size(16, 16).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					if a.Status == subagent.Running {
						ui.Spinner(c).FontSize(12)
					} else {
						ui.Text(c, marker).FontSize(12).TextColor(color)
					}
				})
				ui.Column(c).FillWidth().MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, appmodel.NativeText(textutil.FirstNonEmpty(a.Name, a.ID))).FontSize(12).TextColor(widgets.Ink).SingleLine().Ellipsis("…")
					line := subagent.StatusLabel(a.Status)
					if a.Steps > 0 {
						line += fmt.Sprintf(tr(" · %d steps"), a.Steps)
					}
					if a.Detail != "" {
						line += " · " + a.Detail
					}
					ui.Text(c, appmodel.NativeText(line)).FontSize(12).TextColor(color).Selectable()
				})
			})
		}
	})
}

func subAgentMarker(status string) (string, ui.Color) {
	switch status {
	case subagent.Completed:
		return "✓", widgets.Success
	case subagent.Failed:
		return "✕", widgets.Failed
	case subagent.Running:
		return "◉", widgets.Ink
	case subagent.Interrupted:
		return "■", widgets.Muted
	}
	return "○", widgets.Muted
}
