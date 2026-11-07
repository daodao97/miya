package widgets

import (
	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
)

// SidebarActivity shares activity sections while each platform supplies its
// conversation row, typography and palette.
func SidebarActivity(c *ui.Context, running, completed []model.Thread, fontSize float32, color ui.Color, row func(*ui.Context, model.Thread)) {
	for _, group := range []struct {
		key, label string
		threads    []model.Thread
	}{{"running", tr("In progress"), running}, {"completed", tr("Completed"), completed}} {
		if len(group.threads) == 0 {
			continue
		}
		ui.Column(c.Key(group.key)).FillWidth().Margin(20, 0, 0, 0).Gap(2).Children(func() {
			ui.Text(c, group.label).FontSize(fontSize).TextColor(color).Padding(0, 8, 4, 8)
			for _, thread := range group.threads {
				row(c.Key(thread.ID), thread)
			}
		})
	}
}
