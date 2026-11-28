package nsmobile

import (
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestPhoneComposerKeepsNativePinyinComposition(t *testing.T) {
	if nsgotest.Target() != "ios_simulator" && nsgotest.Target() != "ios" {
		t.Skip("requires a native iOS input method")
	}
	app, m, _ := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		m.Open, m.Path = 7, "/thread"
	})
	const editor = "id:mobile-composer"
	app.Type(editor, "前🙂后")
	app.Select(editor, 3, 3)
	for _, marked := range []string{"n", "ni", "nih", "nihao"} {
		app.Compose(editor, marked)
		if want := "前🙂" + marked + "后"; app.Find(editor).Text != want {
			t.Fatalf("marked %q produced %q, want %q", marked, app.Find(editor).Text, want)
		}
		if app.Enabled("id:send") {
			t.Fatal("send was enabled with an unfinished composition")
		}
	}
	app.Commit(editor, "你好")
	app.Type(editor, "ABC")
	if want := "前🙂你好ABC后"; m.Draft != want || app.Find(editor).Text != want {
		t.Fatalf("draft=%q editor=%q, want %q", m.Draft, app.Find(editor).Text, want)
	}
	if !app.Enabled("id:send") {
		t.Fatal("send remained disabled after committing the candidate")
	}
}
