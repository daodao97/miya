package view

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
)

func TestMessageImagePreview(t *testing.T) {
	img, err := ui.ParseSVG([]byte(`<svg width="160" height="80" viewBox="0 0 160 80"><rect width="160" height="80" fill="#ff0000"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		width int
	}{
		{"desktop", 744},
		{"phone", 320},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &snapshotHost{thread: model.Thread{ID: 1, Messages: []model.Message{
				{ID: 1, Role: "user", Content: "Sent an image", Images: []string{"sent.png"}},
			}}}
			v := NewConversationView(host)
			v.Mobile = tc.name == "phone"
			v.ImagePreview = func(string) ui.ImageSource { return img }
			showConversation := true
			const height = 480
			tt := ui.NewTester(func(c *ui.Context) {
				if showConversation {
					v.MessageList(c, host.thread)
				}
			}, tc.width, height)
			if v.Mobile {
				tt.SetExtendIntoSafeArea(true)
				tt.SetInsets(48, 0, 34, 0)
			}

			open := func() {
				t.Helper()
				if err := tt.Click(tr("Image preview")); err != nil {
					t.Fatal(err)
				}
				if _, ok := tt.Find(tr("Close")); !ok {
					t.Fatal("image dialog did not open")
				}
				if got := tt.Image().RGBAAt(tc.width/2, height/2); got != (color.RGBA{R: 255, A: 255}) {
					t.Fatalf("dialog center did not show the sent image: %v", got)
				}
			}
			assertClosed := func() {
				t.Helper()
				if _, ok := tt.Find(tr("Close")); ok {
					t.Fatal("image dialog remained open")
				}
			}

			open()
			if err := tt.Click(tr("Close")); err != nil {
				t.Fatal(err)
			}
			assertClosed()

			open()
			if v.Mobile {
				tt.ClickAt(4, 4)
			} else {
				tt.Key(0, ui.KeyEscape)
			}
			assertClosed()

			open()
			showConversation = false
			tt.Frame()
			showConversation = true
			tt.Frame()
			assertClosed()
		})
	}
}
