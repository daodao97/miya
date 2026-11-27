package mobile

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
)

func TestMobileMessageOpensDesktopWebLink(t *testing.T) {
	const address = "http://localhost:5173/app?tab=2#top"
	for _, tc := range []struct{ name, role, content, label string }{
		{"markdown link", "assistant", "Preview: [Open preview](" + address + ").", "Open preview"},
		{"plain URL", "assistant", address, address},
		{"inline code", "assistant", "`" + address + "`", address},
		{"inline code with mention", "user", "@[Other](miya://0123456789abcdef0123456789abcdef/thread/claude-code/child) `" + address + "`", address},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
			m := NewMobileApp()
			var opened string
			m.View.Md.OnLink = func(link string) {
				opened = link
				m.openMessageLink(link)
			}
			tt := ui.NewTester(func(c *ui.Context) {
				m.View.MessageView(c, model.Message{ID: 1, Role: tc.role, Content: tc.content}, false)
			}, 390, 250)
			if err := tt.Click(tc.label); err != nil {
				t.Fatal(err)
			}
			if opened != address {
				t.Fatalf("opened %q, want %q", opened, address)
			}
			if want := tr("Connect to your desktop to open web services."); m.SendErr != want {
				t.Fatalf("click did not reach the desktop web opener: got %q", m.SendErr)
			}
		})
	}
}
