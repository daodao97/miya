package nsui

import (
	"path/filepath"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
)

func newConversationIntro(cx *core.Scope, c Host, openProject func(string)) core.Node {
	colors := nswidgets.ColorsOf(cx)
	projectName := Live(func() string {
		s := c.State()
		if s.Projects != nil {
			for _, p := range s.Projects() {
				if p.Path == s.Cwd && p.Name != "" {
					return p.Name
				}
			}
		}
		if s.Cwd != "" {
			return filepath.Base(s.Cwd)
		}
		return ""
	})
	caption := func() string {
		if name := projectName(); name != "" {
			return name
		}
		return tr("Select project")
	}
	return core.View{Style: core.Style{
		FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
		Gap: 12, Padding: core.Edges{Right: 28, Bottom: 40, Left: 28},
	}, Children: []core.Node{
		nswidgets.Icon("brand-openai", 46, colors.Ink),
		core.Text{TestID: "new-conversation-heading", Content: tr("Let’s build"),
			StyleFunc: func() core.Style { return core.Style{FontSize: 28, FontWeight: ui.Semibold, Color: colors.Ink()} }},
		core.View{TestID: "new-conversation-project", Role: core.RoleButton, LabelFunc: caption,
			OnPress: func(core.PressEvent) { openProject(projectName()) },
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Height: core.Pt(36),
				MaxWidth: core.Pct(100), MinWidth: core.Pt(0), Padding: core.Symmetric(0, 8), CornerRadius: 6},
			StyleFunc: func() core.Style {
				return core.Style{Hover: &core.Style{Background: colors.Hover()}, FocusVisible: ui.FocusRing(cx, 2)}
			},
			Children: []core.Node{
				menuText(caption, core.Style{FontSize: 26, FlexShrink: 1}, colors.Muted, 1),
				nswidgets.Icon("chevron-down", 16, colors.Muted),
			}},
	}}
}
