package integration

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/model"
)

func saveMobileNativeSnapshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("MIYA_SNAPSHOT"); dir != "" {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMobileComposerTargetsAndDynamicType(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			th := m.State.Projects[0].Threads[0]
			th.HistoryLoaded = true
			th.Messages = []model.Message{{ID: 1, Role: "assistant", Content: "Readable message"}}
			m.OpenThread(th)
			m.ShowThread(th)
			tt := ui.NewTester(m.ViewRoot, width, 750)
			tt.Frame()
			body, _ := tt.Find("Readable message")
			if err := tt.Click("Message"); err != nil {
				t.Fatal(err)
			}
			tt.Type("短消息")
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("composer-%d", width))
			tt.SetPreferences(ui.Preferences{TextScale: 1.8})
			larger, _ := tt.Find("Readable message")
			if larger.H <= body.H || m.Draft != "短消息" || !tt.Focused("Message") {
				t.Fatalf("text scaling lost size, draft or focus: before=%v after=%v draft=%q", body, larger, m.Draft)
			}
			for _, label := range []string{"Add image", "Model settings", "Send"} {
				r, ok := tt.Find(label)
				if !ok || r.W < 44 || r.H < 44 || r.X < 0 || r.X+r.W > float32(width) || r.Y+r.H > 750 {
					t.Fatalf("%q has an inaccessible target at large text size: %v", label, r)
				}
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("composer-large-%d", width))
			th.Busy = true
			m.ShowThread(th)
			tt.Frame()
			steer, _ := tt.Find("Steer now")
			send, _ := tt.Find("Add to queue")
			model, _ := tt.Find("Model settings")
			if steer.X < model.X+model.W || steer.X+steer.W > send.X || send.X+send.W > float32(width) {
				t.Fatalf("busy controls overlap: model=%v steer=%v send=%v", model, steer, send)
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("composer-busy-large-%d", width))
		})
	}
}

func TestMobileComposerCentersCaretWithTools(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, draft := range []string{"", "短消息"} {
			t.Run(fmt.Sprintf("%d/%q", width, draft), func(t *testing.T) {
				m := testMobileApp()
				m.OpenThread(m.State.Projects[0].Threads[0])
				m.Draft = draft
				tt := ui.NewTester(m.ViewRoot, width, 750)
				tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
				if err := tt.Click("Message"); err != nil {
					t.Fatal(err)
				}
				for _, scale := range []float32{1, 1.8} {
					tt.SetPreferences(ui.Preferences{TextScale: scale})
					caret, active := tt.TextCaret()
					send, ok := tt.Find("Send")
					delta := caret.Y + caret.H/2 - (send.Y + send.H/2)
					if !active || !ok || delta < -2 || delta > 2 {
						t.Fatalf("caret is not centered at text scale %v: caret=%v send=%v", scale, caret, send)
					}
				}
			})
		}
	}
}

func TestMobileNavigationKeepsConversationWhileOpeningDrawer(t *testing.T) {
	m := testMobileApp()
	m.Nav = nil // Exercise production motion instead of the helper's instant navigation.
	m.Remember("s1", []model.Message{{ID: 1, Role: "assistant", Content: "Existing answer"}})
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	if !m.Router().Transitioning() {
		t.Fatal("conversation navigation has no transition")
	}
	tt.Advance(time.Second)
	if !tt.HasText("Existing answer") {
		t.Fatal("conversation did not appear after the transition")
	}
	m.Draft = "未发送的内容"
	openMobileDrawer(t, tt)
	if m.Router().Transitioning() || m.Open != 1 || m.Router().Path() != "/thread" || !m.Menu || m.Draft != "未发送的内容" {
		t.Fatal("opening the drawer navigated away or lost the conversation draft")
	}
	tt.ClickAt(370, 400)
	tt.Advance(time.Second)
	if m.Menu || m.Open != 1 || !tt.HasText("Existing answer") || m.Draft != "未发送的内容" {
		t.Fatal("closing the drawer lost the current conversation or draft")
	}
}

func TestMobileDrawerPagesUseTheSameSlideTransition(t *testing.T) {
	for _, conversation := range []bool{false, true} {
		for _, destination := range []struct{ label, path string }{{"Automations", "/automations"}, {"Settings", "/settings"}} {
			t.Run(fmt.Sprintf("conversation=%v/%s", conversation, destination.path), func(t *testing.T) {
				m := testMobileApp()
				m.Nav = nil // Use the production navigation animation.
				if conversation {
					m.OpenThread(m.State.Projects[0].Threads[0])
				}
				tt := ui.NewTester(m.ViewRoot, 390, 844)
				tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
				tt.Frame()
				openMobileDrawer(t, tt)
				if err := tt.Click(destination.label); err != nil {
					t.Fatal(err)
				}
				start, ok := tt.Find("Open sidebar")
				if !ok || start.X < 390 || !m.Router().Transitioning() || m.Router().Path() != destination.path || m.Menu {
					t.Fatalf("drawer navigation should slide the page in from the right: header=%v path=%s", start, m.Router().Path())
				}
				tt.Advance(70 * time.Millisecond)
				middle, _ := tt.Find("Open sidebar")
				if middle.X <= 12 || middle.X >= start.X {
					t.Fatalf("the new page did not move during its transition: start=%v middle=%v", start, middle)
				}
				tt.Advance(time.Second)
				end, _ := tt.Find("Open sidebar")
				if end.X != 12 || end.Y < 59 || end.Y+end.H > 810 || m.Router().Transitioning() {
					t.Fatalf("the page did not settle within the safe area: %v", end)
				}
			})
		}
	}
}

func TestMobileConversationEdgeOpensDrawer(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			m.Router().InteractiveBack = true // The native iOS router default.
			m.Remember("s1", []model.Message{{ID: 10, Role: "assistant", Content: "已有的回复"}})
			m.OpenThread(m.State.Projects[0].Threads[0])
			m.Draft = "保留草稿"
			tt := ui.NewTester(m.ViewRoot, width, 750)
			tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
			tt.Frame()
			if m.Router().InteractiveBack {
				t.Fatal("native swipe back would steal the conversation drawer gesture")
			}
			dragOver(tt, 8, 40, 350, 120*time.Millisecond)
			if m.Menu || m.Open != 1 || m.Router().Path() != "/thread" {
				t.Fatal("a cancelled edge drag should stay in the current conversation")
			}
			drag(tt, 8, float32(width)-40, 350)
			if !m.Menu || m.Open != 1 || m.Router().Path() != "/thread" || m.Viewer.Current().ServerID != "s1" || m.Draft != "保留草稿" {
				t.Fatal("the edge swipe should open the drawer and preserve the conversation and draft")
			}
			if _, ok := tt.Find("Project proj-a"); !ok {
				t.Fatal("the edge swipe did not reveal the project drawer")
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("conversation-edge-drawer-%d", width))
		})
	}
}

func TestMobileNavigationKeepsHomeGeometryStable(t *testing.T) {
	m := testMobileApp()
	m.Nav = nil
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
	header, _ := tt.Find("Miya")
	intro, _ := tt.Find("Let’s build")
	composer, _ := tt.Find("Message")
	check := func(phase string) {
		t.Helper()
		h, headerOK := tt.Find("Miya")
		i, introOK := tt.Find("Let’s build")
		input, inputOK := tt.Find("Message")
		if !headerOK || !introOK || !inputOK || h.Y != header.Y || h.H != header.H || i.Y != intro.Y || i.H != intro.H || input.Y != composer.Y || input.H != composer.H {
			t.Fatalf("%s moved the new-conversation page: header %v → %v, intro %v → %v, composer %v → %v", phase, header, h, intro, i, composer, input)
		}
	}
	m.OpenThread(m.State.Projects[0].Threads[0])
	tt.Frame()
	tt.Advance(time.Second)
	openMobileDrawer(t, tt)
	if err := tt.Click("New thread"); err != nil {
		t.Fatal(err)
	}
	check("new conversation")
	tt.Advance(time.Second)
	check("returned")
	tt.Frame()
	check("after cleanup")
}
