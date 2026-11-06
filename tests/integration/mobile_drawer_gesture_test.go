package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
)

func TestMobileDrawerConversationListSwipeCloses(t *testing.T) {
	for _, search := range []bool{false, true} {
		t.Run(fmt.Sprint("search=", search), func(t *testing.T) {
			m := testMobileApp()
			m.Draft = "keep draft"
			tt := ui.NewTester(m.ViewRoot, 390, 750)
			tt.Frame()
			openMobileDrawer(t, tt)
			if search {
				if err := tt.Click("Search conversations"); err != nil {
					t.Fatal(err)
				}
			}
			row, ok := tt.Find("thread-one")
			if !ok {
				t.Fatal("conversation missing from drawer")
			}
			x, y := row.X+row.W/2, row.Y+row.H/2
			// TouchScroll delivers a leftward finger movement as positive DX.
			tt.Scroll(x, y, 40, 2)
			tt.Advance(time.Second)
			if m.Menu || m.DrawerVisible || m.Open != 0 || m.Router().Path() != "/" || m.Draft != "keep draft" {
				t.Fatal("swiping the conversation row did not close only the drawer")
			}
		})
	}
}

func TestMobileDrawerConversationListKeepsVerticalScrollAndTaps(t *testing.T) {
	m := testMobileApp()
	m.ThreadListExpanded = map[string]bool{"/a": true}
	for i := 3; i < 25; i++ {
		m.State.Projects[0].Threads = append(m.State.Projects[0].Threads, model.Thread{ID: i, Title: fmt.Sprintf("conversation-%02d", i)})
	}
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	openMobileDrawer(t, tt)
	row, ok := tt.Find("thread-one")
	if !ok {
		t.Fatal("conversation missing from drawer")
	}
	x, y := row.X+row.W/2, row.Y+row.H/2
	for _, delta := range []ui.Point{{X: 3}, {X: -40}, {X: 20, Y: 100}} {
		tt.Scroll(x, y, delta.X, delta.Y)
		if !m.Menu || m.Open != 0 {
			t.Fatal("small, rightward or vertical movement dismissed the drawer or selected a conversation")
		}
	}
	moved, _ := tt.Find("thread-one")
	if moved.Y >= row.Y-50 {
		t.Fatalf("vertical movement did not scroll the list: before=%v after=%v", row, moved)
	}
	tt.Scroll(x, y, 0, -100)
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	if m.Open != 1 || m.Menu || m.Router().Path() != "/thread" {
		t.Fatal("conversation tap did not open the conversation")
	}
}
