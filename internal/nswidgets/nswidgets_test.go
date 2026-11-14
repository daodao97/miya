package nswidgets

import (
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/iconassets"
)

func TestThemeHoldsMiyasColors(t *testing.T) {
	th := Theme()
	for _, tc := range []struct {
		name string
		p    core.Palette
		want palette
	}{{"light", th.Light, light}, {"dark", th.Dark, dark}} {
		if tc.p.Background != tc.want.canvas || tc.p.Text != tc.want.ink || tc.p.TextMuted != tc.want.muted || tc.p.Border != tc.want.line {
			t.Errorf("%s: standard roles do not hold Miya's colors", tc.name)
		}
		if tc.p.Accent != tc.want.ink || tc.p.AccentText != tc.want.canvas {
			t.Errorf("%s: accent is not Miya's ink", tc.name)
		}
		for name, want := range map[string]core.Color{RoleSide: tc.want.side, RoleWarnBg: tc.want.warnBg, RoleLink: tc.want.link, RoleShadow: tc.want.shadow} {
			if got, ok := tc.p.Color(name); !ok || got != want {
				t.Errorf("%s: role %s = %v, %v; want %v", tc.name, name, got, ok, want)
			}
		}
	}
	// Theme must not change NSGo's default theme, which others share.
	if _, ok := core.DefaultTheme().Light.Color(RoleSide); ok {
		t.Error("Theme wrote into core.DefaultTheme's palette")
	}
}

func TestEveryIconParses(t *testing.T) {
	names := iconassets.Names()
	if len(names) < 30 {
		t.Fatalf("only %d icons embedded", len(names))
	}
	for _, name := range names {
		data, err := iconassets.SVG(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := core.ParseSVG(data); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if SVG("check") != SVG("check") {
		t.Error("icons are parsed again on each use")
	}
}

// Colors read the theme of their scope, in the appearance it shows.
func TestColorsFollowAppearance(t *testing.T) {
	got := map[string]core.Color{}
	nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(Theme())
		probe := func(name string, a core.Appearance) core.Node {
			return core.Func(func(cx *core.Scope) core.Node {
				cx.SetSubtreeAppearance(a)
				colors := ColorsOf(cx)
				got[name+".ink"] = core.Untracked(cx, colors.Ink)
				got[name+".side"] = core.Untracked(cx, colors.Side)
				return core.View{}
			})
		}
		return core.View{Children: []core.Node{probe("light", core.AppearanceLight), probe("dark", core.AppearanceDark)}}
	}, nsgotest.Size(200, 200))
	for key, want := range map[string]core.Color{"light.ink": light.ink, "light.side": light.side, "dark.ink": dark.ink, "dark.side": dark.side} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
}

func TestToolButtonPresses(t *testing.T) {
	pressed := 0
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(Theme())
		return core.View{Children: []core.Node{
			ToolButton("search", "Search", func() { pressed++ }),
			WorkingLabel(cx, "clock", func() string { return "Working" }),
			MutedText(cx, func() string { return "quiet" }),
		}}
	}, nsgotest.Size(300, 200))
	app.Press("id:tool-search")
	app.Settle()
	if pressed != 1 {
		t.Fatalf("pressed %d times, want 1", pressed)
	}
	texts := map[string]bool{}
	for _, s := range app.Texts() {
		texts[s] = true
	}
	if !texts["Working"] || !texts["quiet"] {
		t.Fatalf("texts = %v", app.Texts())
	}
}
