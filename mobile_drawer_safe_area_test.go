package main

import (
	"fmt"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
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
				tt := ui.NewTester(m.viewRoot, width, height)
				tt.SetDark(dark)
				tt.SetSafeArea(safe)
				tt.Frame()
				home := m.homeBackground()
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
				if header, ok := tt.Find("桌面端连接"); !ok || header.Y < safe.Top || header.X < safe.Left {
					t.Fatalf("home controls entered unsafe area: %v", header)
				}
				m.menu = true
				tt.Frame()
				tt.Advance(100 * time.Millisecond)
				for _, phase := range []string{"opening", "open"} {
					if phase == "open" {
						tt.Advance(time.Second)
					}
					drawer := m.pal.drawer
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
					scrim, ok := tt.Find("关闭侧边栏")
					if !ok || scrim.Y != 0 || scrim.Y+scrim.H != float32(height) {
						t.Fatalf("%s scrim does not cover system chrome: %v", phase, scrim)
					}
					top, bottom := tt.Image().RGBAAt(width-5, 5), tt.Image().RGBAAt(width-5, height-5)
					if top != bottom || !dark && top == wantHome {
						t.Fatalf("%s scrim has a seam: top=%v bottom=%v home=%v", phase, top, bottom, wantHome)
					}
					saveMobileNativeSnapshot(t, tt, fmt.Sprintf("drawer-%s-dark-%v-landscape-%v", phase, dark, landscape))
				}
				if button, ok := tt.Find("更换桌面端"); !ok || button.Y < safe.Top || button.Y+button.H > float32(height)-safe.Bottom || button.X < safe.Left {
					t.Fatalf("drawer controls entered unsafe area: %v", button)
				}
				m.menu = false
				tt.Frame()
				tt.Advance(time.Second)
				checkClosed()
			})
		}
	}
}
