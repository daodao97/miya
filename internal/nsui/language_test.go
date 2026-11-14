package nsui

import (
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/locales"
)

// A Chinese system shows the interface in Chinese from the first frame, not
// only once the settings open.
func TestInterfaceFollowsSystemLanguage(t *testing.T) {
	t.Cleanup(func() { locales.SetCurrent("") })
	locales.SetCurrent("")
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return core.Dynamic(func(cx *core.Scope) core.Node {
			interfaceLanguage(cx)
			return core.Text{TestID: "label", Content: tr("New conversation")}
		})
	}, nsgotest.Size(300, 100), nsgotest.Environment(core.Environment{OS: core.OSMacOS, Locale: "zh-Hans-CN"}))
	want := locales.TextIn("zh-Hans", "New conversation")
	if want == "New conversation" {
		t.Fatal("catalog has no Chinese for the label")
	}
	if got := app.Find("id:label").Text; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
}
