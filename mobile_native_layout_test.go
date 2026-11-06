package main

import (
	fmt "fmt"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func TestMobileActionsKeepCancelVisibleAndScroll(t *testing.T) {
	for _, size := range []struct {
		width, height int
		safe          ui.SafeArea
	}{
		{852, 393, ui.SafeArea{Left: 59, Right: 59, Bottom: 21}},
		{320, 750, ui.SafeArea{Top: 59, Bottom: 34}},
	} {
		for _, scale := range []float32{1, 3} {
			t.Run(fmt.Sprintf("%dx%d/%v", size.width, size.height, scale), func(t *testing.T) {
				m := testMobileApp()
				m.OpenThread(m.State.Projects[0].Threads[0])
				m.Draft = "keep draft"
				tt := ui.NewTester(m.ViewRoot, size.width, size.height)
				tt.SetSafeArea(size.safe)
				tt.SetPreferences(ui.Preferences{TextScale: scale})
				if err := tt.Click("More"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				cancel, ok := tt.Find("Cancel")
				if !ok || cancel.H < 56 || cancel.Y < size.safe.Top+12 || cancel.Y+cancel.H > float32(size.height)-size.safe.Bottom-8 {
					t.Fatalf("Cancel leaves the safe area: %v", cancel)
				}
				first, ok := tt.Find("Project files")
				if !ok || first.H < 44 || first.Y < size.safe.Top+12 {
					t.Fatalf("first action is not reachable: %v", first)
				}
				tt.Scroll(first.X+first.W/2, first.Y+first.H/2, 0, 2000)
				last, ok := tt.Find("New conversation")
				if !ok || last.H < 44 || last.Y+last.H > cancel.Y-8+0.1 {
					t.Fatalf("last action cannot scroll above Cancel: %v, Cancel=%v", last, cancel)
				}
				after, _ := tt.Find("Cancel")
				if after != cancel {
					t.Fatalf("scrolling moved Cancel: %v -> %v", cancel, after)
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("native-actions-%dx%d-%v", size.width, size.height, scale))
				if err := tt.Click("Cancel"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if m.ConversationMenu || m.Open != 1 || m.Draft != "keep draft" {
					t.Fatal("Cancel changed the conversation or draft")
				}
			})
		}
	}
}

func TestMobileLanguageChoicesFitLargeText(t *testing.T) {
	for _, width := range []int{320, 393} {
		m := testMobileApp()
		m.OpenDrawerPage("/settings")
		tt := ui.NewTester(m.ViewRoot, width, 852)
		tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
		tt.SetPreferences(ui.Preferences{TextScale: 3})
		tt.Frame()
		var bottom float32
		for _, label := range []string{"Follow system", "简体中文", "English"} {
			r, ok := tt.Find(label)
			if !ok || r.H < 44 || r.X < 16 || r.X+r.W > float32(width)-16 || r.Y < bottom || r.Y+r.H > 818 {
				t.Fatalf("language %q is clipped at width %d: %v", label, width, r)
			}
			bottom = r.Y + r.H
		}
		saveMobileNativeSnapshot(t, tt, fmt.Sprintf("native-language-large-%d", width))
	}
}

func TestMobileSwitchHasFullHeightTouchTarget(t *testing.T) {
	m := testMobileApp()
	on := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			_ = m.PhoneSwitch(c, &on, "Notifications").Changed()
		})
	}, 200, 100)
	tt.Frame()
	r, ok := tt.Find("Notifications")
	if !ok || r.W < 44 || r.H < 44 {
		t.Fatalf("switch target is too small: %v", r)
	}
	// This point is above the visible track but inside its touch target.
	tt.ClickAt(r.X+2, r.Y+2)
	if !on {
		t.Fatal("switch did not respond outside the track within its target")
	}
}

func TestMobileDrawerSearchReturnKeepsResultsAndDismissesInput(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 393, 852)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("Search conversations"); err != nil {
		t.Fatal(err)
	}
	tt.Type("thread")
	tt.Key(0, ui.KeyEnter)
	if m.DrawerQuery != "thread" || tt.Focused("Search conversations") || !m.Menu || m.Open != 0 || !tt.HasText("thread-one") {
		t.Fatal("search return changed the query, selected a conversation or retained input focus")
	}
}

func TestMobileMemoryScopeShowsSelection(t *testing.T) {
	m := testMobileApp()
	m.State.Memory = true
	m.OpenDrawerPage("/memory")
	tt := ui.NewTester(m.ViewRoot, 393, 852)
	tt.Frame()
	project, _ := tt.Find("Current project")
	user, _ := tt.Find("User preferences")
	pixel := func(r ui.Rect) [4]uint8 {
		c := tt.Image().RGBAAt(int(r.X+8), int(r.Y+r.H/2))
		return [4]uint8{c.R, c.G, c.B, c.A}
	}
	selected, unselected := pixel(project), pixel(user)
	if selected == unselected {
		t.Fatal("current memory scope has no visible selection")
	}
	if err := tt.Click("User preferences"); err != nil {
		t.Fatal(err)
	}
	if m.Memories.Scope != "user" || pixel(project) != unselected || pixel(user) != selected {
		t.Fatal("switching scope did not update the selection indicator")
	}
}

func TestMobileDrawerLargeTextWrapsConversationTitles(t *testing.T) {
	m := testMobileApp()
	title := "A conversation title that stays distinguishable when accessibility text is enlarged"
	m.State.Projects[0].Threads[0].Title = title
	tt := ui.NewTester(m.ViewRoot, 393, 852)
	tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
	tt.SetPreferences(ui.Preferences{TextScale: 2})
	tt.Frame()
	openMobileDrawer(t, tt)
	r, ok := tt.Find(title)
	if !ok || r.H < m.Font(16)*2 {
		t.Fatalf("large conversation title is still restricted to one line: %v", r)
	}
	saveMobileNativeSnapshot(t, tt, "native-drawer-title-large")
}
