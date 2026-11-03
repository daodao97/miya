package main

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
				m.openThread(m.state.Projects[0].Threads[0])
				m.draft = "保留草稿"
				tt := ui.NewTester(m.viewRoot, width, 844)
				tt.SetDark(dark)
				tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
				tt.SetPreferences(ui.Preferences{TextScale: 1.8})
				tt.Frame()
				open := func() {
					t.Helper()
					if err := tt.Click("更多"); err != nil {
						t.Fatal(err)
					}
					tt.Advance(time.Second)
				}
				open()
				files, filesOK := tt.Find("项目文件")
				web, webOK := tt.Find("Web 服务")
				newThread, newOK := tt.Find("新对话")
				cancel, cancelOK := tt.Find("取消")
				if !filesOK || !webOK || !newOK || !cancelOK || files.Y < 71 || files.H < 58 || web.H < 58 || web.Y+0.1 < files.Y+files.H || newThread.Y+0.1 < web.Y+web.H || cancel.Y+0.1 < newThread.Y+newThread.H || cancel.Y+cancel.H > 810 || cancel.X < 12 || cancel.X+cancel.W > float32(width)-12 {
					t.Fatalf("sheet actions overlap or leave the bottom safe area: files=%v web=%v new=%v cancel=%v", files, web, newThread, cancel)
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("conversation-actions-%d-dark-%v", width, dark))
				tt.SetPreferences(ui.Preferences{TextScale: 1})
				tt.Frame()
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("conversation-actions-normal-%d-dark-%v", width, dark))
				tt.SetPreferences(ui.Preferences{TextScale: 1.8})
				tt.Frame()
				if err := tt.Click("取消"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if tt.HasText("项目文件") || m.draft != "保留草稿" || m.open != 1 {
					t.Fatal("cancel should dismiss the sheet and preserve the current conversation and draft")
				}
				open()
				tt.ClickAt(10, 120)
				tt.Advance(time.Second)
				if tt.HasText("项目文件") || m.draft != "保留草稿" {
					t.Fatal("backdrop should dismiss without changing the draft")
				}
				open()
				if err := tt.Click("新对话"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if m.router().Path() != "/" || m.open != 0 || m.composerProject() != "/a" || m.draft != "" || tt.HasText("项目文件") {
					t.Fatal("new conversation should dismiss the sheet and compose in the same project")
				}
			})
		}
	}
}
