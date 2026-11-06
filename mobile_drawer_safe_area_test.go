package main

import (
	fmt "fmt"
	color "image/color"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func TestMobileDrawerBackgroundCoversSafeArea(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, landscape := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/landscape=%v", dark, landscape), func(t *testing.T) {
				width, height := 390, 844
				safe := ui.SafeArea{Top: 59, Bottom: 34}
				if landscape {
					width, height = 844, 390
					safe = ui.SafeArea{Left: 59, Right: 59, Bottom: 21}
				}
				m := mobileHomeFixture()
				tt := ui.NewTester(m.ViewRoot, width, height)
				tt.SetDark(dark)
				tt.SetSafeArea(safe)
				tt.Frame()
				home := m.HomeBackground()
				wantHome := color.RGBA{R: home.R, G: home.G, B: home.B, A: 255}
				checkClosed := func() {
					t.Helper()
					for _, point := range [][2]int{{5, 5}, {width - 5, 5}, {5, height - 5}, {width - 5, height - 5}} {
						if got := tt.Image().RGBAAt(point[0], point[1]); got != wantHome {
							t.Fatalf("closed safe area %v: %v want %v", point, got, wantHome)
						}
					}
				}
				checkClosed()
				if header, ok := tt.Find("Desktop connections"); !ok || header.Y < safe.Top || header.X < safe.Left {
					t.Fatalf("home controls entered unsafe area: %v", header)
				}
				m.Menu = true
				tt.Frame()
				tt.Advance(100 * time.Millisecond)
				for _, phase := range []string{"opening", "open"} {
					if phase == "open" {
						tt.Advance(time.Second)
					}
					drawer := m.Pal.Drawer
					wantDrawer := color.RGBA{R: drawer.R, G: drawer.G, B: drawer.B, A: 255}
					// While sliding, navigation rows can pass through the
					// left edge; check that edge's middle once settled.
					samples := []int{5, height - 5}
					if phase == "open" {
						samples = append(samples, height/2)
					}
					for _, y := range samples {
						if got := tt.Image().RGBAAt(5, y); got != wantDrawer {
							t.Fatalf("%s drawer background stops at safe area y=%d: %v want %v", phase, y, got, wantDrawer)
						}
					}
					scrim, ok := tt.Find("Close sidebar")
					if !ok || scrim.Y != 0 || scrim.Y+scrim.H != float32(height) {
						t.Fatalf("%s scrim does not cover system chrome: %v", phase, scrim)
					}
					top, bottom := tt.Image().RGBAAt(width-5, 5), tt.Image().RGBAAt(width-5, height-5)
					if top != bottom || !dark && top == wantHome {
						t.Fatalf("%s scrim has a seam: top=%v bottom=%v home=%v", phase, top, bottom, wantHome)
					}
					saveMobileNativeSnapshot(t, tt, fmt.Sprintf("drawer-%s-dark-%v-landscape-%v", phase, dark, landscape))
				}
				if button, ok := tt.Find("Settings"); !ok || button.Y < safe.Top || button.Y+button.H > float32(height)-safe.Bottom || button.X < safe.Left {
					t.Fatalf("drawer controls entered unsafe area: %v", button)
				}
				m.Menu = false
				tt.Frame()
				tt.Advance(time.Second)
				checkClosed()
			})
		}
	}
}

func TestMobileDrawerClosingFramesMatchRestingPage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, conversation := range []bool{false, true} {
			for _, closeWith := range []string{"tap", "drag"} {
				t.Run(fmt.Sprintf("dark=%v/conversation=%v/%s", dark, conversation, closeWith), func(t *testing.T) {
					m := testMobileApp()
					if conversation {
						m.OpenThread(m.State.Projects[0].Threads[0])
					}
					m.Draft = "保留草稿"
					tt := ui.NewTester(m.ViewRoot, 390, 844)
					tt.SetDark(dark)
					tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
					tt.Frame()
					points := [][2]int{{5, 5}, {374, 422}}
					want := make([]color.RGBA, len(points))
					for i, point := range points {
						want[i] = tt.Image().RGBAAt(point[0], point[1])
					}
					path := m.Router().Path()
					m.Menu = true
					tt.Frame()
					tt.Advance(time.Second)
					if closeWith == "tap" {
						tt.ClickAt(370, 400)
					} else {
						tt.Press(370, 400)
						tt.Frame()
						for step := 1; step <= 8; step++ {
							tt.Advance(120 * time.Millisecond)
							tt.Move(370-270*float32(step)/8, 400)
							tt.Frame()
						}
						tt.Release(100, 400)
						tt.Frame()
					}
					nearClosed := false
					for frame := 0; frame < 90; frame++ {
						tt.Advance(16 * time.Millisecond)
						scrim, visible := tt.Find("Close sidebar")
						if visible && scrim.X <= 2 {
							nearClosed = true
							for i, point := range points {
								got := tt.Image().RGBAAt(point[0], point[1])
								for channel := 0; channel < 3; channel++ {
									a, b := []uint8{got.R, got.G, got.B}[channel], []uint8{want[i].R, want[i].G, want[i].B}[channel]
									if delta := int(a) - int(b); delta < -2 || delta > 2 {
										t.Fatalf("page changes color as the drawer settles at x=%v: pixel %v = %v, resting page = %v", scrim.X, point, got, want[i])
									}
								}
							}
						}
						if !visible {
							break
						}
					}
					if !nearClosed || m.Menu || m.DrawerVisible || m.Router().Path() != path || m.Draft != "保留草稿" {
						t.Fatal("closing the drawer skipped its settling frames or changed the page/draft")
					}
				})
			}
		}
	}
}
