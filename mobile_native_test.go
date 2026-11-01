package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
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
			th := m.state.Projects[0].Threads[0]
			th.HistoryLoaded = true
			th.Messages = []Message{{ID: 1, Role: "assistant", Content: "Readable message"}}
			m.openThread(th)
			m.showThread(th)
			tt := ui.NewTester(m.viewRoot, width, 750)
			tt.Frame()
			body, _ := tt.Find("Readable message")
			if err := tt.Click("消息"); err != nil {
				t.Fatal(err)
			}
			tt.Type("短消息")
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("composer-%d", width))
			tt.SetPreferences(ui.Preferences{TextScale: 1.8})
			larger, _ := tt.Find("Readable message")
			if larger.H <= body.H || m.draft != "短消息" || !tt.Focused("消息") {
				t.Fatalf("text scaling lost size, draft or focus: before=%v after=%v draft=%q", body, larger, m.draft)
			}
			for _, label := range []string{"添加图片", "模型设置", "发送"} {
				r, ok := tt.Find(label)
				if !ok || r.W < 44 || r.H < 44 || r.X < 0 || r.X+r.W > float32(width) || r.Y+r.H > 750 {
					t.Fatalf("%q has an inaccessible target at large text size: %v", label, r)
				}
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("composer-large-%d", width))
			th.Busy = true
			m.showThread(th)
			tt.Frame()
			steer, _ := tt.Find("立即引导")
			send, _ := tt.Find("加入队列")
			model, _ := tt.Find("模型设置")
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
				m.openThread(m.state.Projects[0].Threads[0])
				m.draft = draft
				tt := ui.NewTester(m.viewRoot, width, 750)
				tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
				if err := tt.Click("消息"); err != nil {
					t.Fatal(err)
				}
				for _, scale := range []float32{1, 1.8} {
					tt.SetPreferences(ui.Preferences{TextScale: scale})
					caret, active := tt.TextCaret()
					send, ok := tt.Find("发送")
					delta := caret.Y + caret.H/2 - (send.Y + send.H/2)
					if !active || !ok || delta < -2 || delta > 2 {
						t.Fatalf("caret is not centered at text scale %v: caret=%v send=%v", scale, caret, send)
					}
				}
			})
		}
	}
}

func TestMobileNavigationKeepsOutgoingConversationUntilAnimationEnds(t *testing.T) {
	m := testMobileApp()
	m.nav = nil // Exercise production motion instead of the helper's instant navigation.
	m.remember("s1", []Message{{ID: 1, Role: "assistant", Content: "Existing answer"}})
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	if !m.router().Transitioning() {
		t.Fatal("conversation navigation has no transition")
	}
	tt.Advance(time.Second)
	if !tt.HasText("Existing answer") {
		t.Fatal("conversation did not appear after the transition")
	}
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	if !m.router().Transitioning() || m.open != 1 || tt.HasText("Let’s build") {
		t.Fatal("back animation discarded the outgoing conversation")
	}
	tt.Advance(time.Second)
	if m.open != 0 || m.router().Transitioning() || !tt.HasText("thread-one") {
		t.Fatal("back animation did not release the conversation and return to the list")
	}
}

func TestMobileNavigationKeepsHomeGeometryStable(t *testing.T) {
	for _, scrolled := range []bool{false, true} {
		t.Run(fmt.Sprintf("scrolled=%v", scrolled), func(t *testing.T) {
			m := testMobileApp()
			m.nav = nil
			for i := 3; i < 25; i++ {
				m.state.Projects[0].Threads = append(m.state.Projects[0].Threads, Thread{ID: i, Title: fmt.Sprintf("session-%02d", i), ServerID: fmt.Sprint(i)})
			}
			tt := ui.NewTester(m.viewRoot, 390, 844)
			tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
			if scrolled {
				tt.Scroll(200, 500, 0, 216)
			}
			header, _ := tt.Find("Miya")
			row, _ := tt.Find("session-08")
			// Leaving pages are inert and absent from Tester's labels. At
			// progress zero they still paint at their original horizontal
			// position, so verify the heading's ink stays in its old bounds.
			headerInk := func() int {
				img, count := tt.Image(), 0
				for y := int(header.Y); y < int(header.Y+header.H); y++ {
					for x := int(header.X); x < int(header.X+header.W); x++ {
						p := img.RGBAAt(x, y)
						if p.R < 80 && p.G < 80 && p.B < 80 {
							count++
						}
					}
				}
				return count
			}
			ink := headerInk()
			if ink == 0 {
				t.Fatal("home heading was not painted")
			}
			check := func(phase string) {
				t.Helper()
				h, ok := tt.Find("Miya")
				r, rowOK := tt.Find("session-08")
				if !ok || !rowOK || h.Y != header.Y || h.H != header.H || r.Y != row.Y || r.H != row.H {
					t.Fatalf("%s moved home vertically: header %v → %v, row %v → %v", phase, header, h, row, r)
				}
			}
			if err := tt.Click("session-08"); err != nil {
				t.Fatal(err)
			}
			if got := headerInk(); got != ink {
				t.Fatalf("opening detail shifted the outgoing heading: %d ink pixels left, want %d", got, ink)
			}
			tt.Advance(time.Second)
			if err := tt.Click("返回"); err != nil {
				t.Fatal(err)
			}
			check("starting back")
			tt.Advance(70 * time.Millisecond)
			check("returning")
			tt.Advance(time.Second)
			check("returned")
			tt.Frame()
			check("after cleanup")
		})
	}
}
