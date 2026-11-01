package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestMobileDrawerDestinationsReturnToDrawer(t *testing.T) {
	for _, fromConversation := range []bool{false, true} {
		for _, destination := range []struct{ label, path string }{
			{"thread-one", "/thread"}, {"自动化", "/automations"}, {"设置", "/settings"},
		} {
			t.Run(fmt.Sprintf("fromConversation=%v/%s", fromConversation, destination.path), func(t *testing.T) {
				m := testMobileApp()
				if fromConversation {
					m.openThread(m.state.Projects[0].Threads[0])
				}
				tt := ui.NewTester(m.viewRoot, 390, 844)
				tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
				openMobileDrawer(t, tt)
				if err := tt.Click(destination.label); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if m.router().Path() != destination.path || m.menu || m.router().InteractiveBack {
					t.Fatal("drawer destination should own its drawer and edge gesture")
				}
				m.draft = "保留当前页面的草稿"
				history := len(m.router().History())
				openMobileDrawer(t, tt)
				if !m.menu || m.router().Path() != destination.path || len(m.router().History()) != history || m.draft != "保留当前页面的草稿" {
					t.Fatal("the arrow should reveal the drawer without changing history or page state")
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("drawer-return-%v-%s", fromConversation, destination.label))
				tt.ClickAt(370, 400)
				tt.Advance(time.Second)
				if m.menu || m.router().Path() != destination.path {
					t.Fatal("dismissing the drawer should reveal the same destination")
				}
				dragOver(tt, 8, 40, 350, 120*time.Millisecond)
				if m.menu || m.router().Path() != destination.path {
					t.Fatal("a cancelled edge swipe should keep the same destination")
				}
				drag(tt, 8, 300, 350)
				if !m.menu || m.router().Path() != destination.path || len(m.router().History()) != history || m.draft != "保留当前页面的草稿" {
					t.Fatal("an edge swipe should reveal the drawer without navigating back")
				}
			})
		}
	}
}

func TestMobileDrawerChildPagesReturnToTheirParent(t *testing.T) {
	for _, destination := range []struct{ label, path, child, childPath string }{
		{"设置", "/settings", "模型与权限", "/options"},
		{"自动化", "/automations", "仓库日报", "/automation"},
	} {
		t.Run(destination.path, func(t *testing.T) {
			m := testMobileApp()
			m.state.Automations = true
			data, _ := json.Marshal([]automationView{mobileAutomationFixture()})
			m.receiveAutomations(data, "")
			tt := ui.NewTester(m.viewRoot, 390, 844)
			tt.Frame()
			openMobileDrawer(t, tt)
			if err := tt.Click(destination.label); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click(destination.child); err != nil {
				t.Fatal(err)
			}
			if m.router().Path() != destination.childPath || !m.router().InteractiveBack || tt.HasText("打开侧边栏") {
				t.Fatal("a child page should use ordinary back navigation")
			}
			if err := tt.Click("返回"); err != nil {
				t.Fatal(err)
			}
			if m.router().Path() != destination.path || m.menu || m.router().InteractiveBack {
				t.Fatal("a child page should return to its parent without opening the drawer")
			}
			openMobileDrawer(t, tt)
			if !m.menu || m.router().Path() != destination.path {
				t.Fatal("the parent's back arrow should reveal the drawer")
			}
		})
	}
}
