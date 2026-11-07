package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestMobileConversationActionsSheet(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/dark=%v", width, dark), func(t *testing.T) {
				m := testMobileApp()
				m.OpenThread(m.State.Projects[0].Threads[0])
				m.Draft = "保留草稿"
				tt := ui.NewTester(m.ViewRoot, width, 844)
				tt.SetDark(dark)
				tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
				tt.SetPreferences(ui.Preferences{TextScale: 1.8})
				tt.Frame()
				open := func() {
					t.Helper()
					if err := tt.Click("More"); err != nil {
						t.Fatal(err)
					}
					tt.Advance(time.Second)
				}
				open()
				files, filesOK := tt.Find("Project files")
				web, webOK := tt.Find("Web services")
				newThread, newOK := tt.Find("New conversation")
				cancel, cancelOK := tt.Find("Cancel")
				if !filesOK || !webOK || !newOK || !cancelOK || files.Y < 71 || files.H < 58 || web.H < 58 || web.Y+0.1 < files.Y+files.H || newThread.Y+0.1 < web.Y+web.H || cancel.Y+0.1 < newThread.Y+newThread.H || cancel.Y+cancel.H > 810 || cancel.X < 12 || cancel.X+cancel.W > float32(width)-12 {
					t.Fatalf("sheet actions overlap or leave the bottom safe area: files=%v web=%v new=%v cancel=%v", files, web, newThread, cancel)
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("conversation-actions-%d-dark-%v", width, dark))
				tt.SetPreferences(ui.Preferences{TextScale: 1})
				tt.Frame()
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("conversation-actions-normal-%d-dark-%v", width, dark))
				tt.SetLocale("zh-Hans")
				tt.Advance(time.Second)
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("conversation-actions-zh-%d-dark-%v", width, dark))
				tt.SetLocale("en")
				tt.SetPreferences(ui.Preferences{TextScale: 1.8})
				tt.Frame()
				if err := tt.Click("Cancel"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if tt.HasText("Project files") || m.Draft != "保留草稿" || m.Open != 1 {
					t.Fatal("cancel should dismiss the sheet and preserve the current conversation and draft")
				}
				open()
				tt.ClickAt(10, 120)
				tt.Advance(time.Second)
				if tt.HasText("Project files") || m.Draft != "保留草稿" {
					t.Fatal("backdrop should dismiss without changing the draft")
				}
				open()
				if err := tt.Click("New conversation"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if m.Router().Path() != "/" || m.Open != 0 || m.ComposerProject() != "/a" || m.Draft != "" || tt.HasText("Project files") {
					t.Fatal("new conversation should dismiss the sheet and compose in the same project")
				}
			})
		}
	}
}
