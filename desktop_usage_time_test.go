package main

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/agentusage"
)

func TestUsageTimeProgress(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		mins  int64
		reset time.Time
		want  float64
		valid bool
	}{
		{"five hours", 300, now.Add(207 * time.Minute), 31, true},
		{"week", 10080, now.Add(84 * time.Hour), 50, true},
		{"before start", 300, now.Add(6 * time.Hour), 0, true},
		{"reset reached", 300, now, 100, true},
		{"stale window", 300, now.Add(-time.Hour), 100, true},
		{"missing duration", 0, now.Add(time.Hour), 0, false},
		{"invalid duration", -1, now.Add(time.Hour), 0, false},
		{"missing reset", 300, time.Time{}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset := int64(0)
			if !tc.reset.IsZero() {
				reset = tc.reset.Unix()
			}
			got, valid := usageTimeProgress(agentusage.Window{DurationMins: tc.mins, ResetsAt: reset}, now)
			if valid != tc.valid || math.Abs(got-tc.want) > 0.001 {
				t.Fatalf("got %v, %v; want %v, %v", got, valid, tc.want, tc.valid)
			}
		})
	}
	if usagePaceText(2, 31) != "均速 31% · 少用 29 个百分点" || usagePaceText(60, 40) != "均速 40% · 多用 20 个百分点" || usagePaceText(31, 31) != "均速 31% · 与时间持平" {
		t.Fatal("pacing comparison has the wrong direction")
	}
}

func TestUsagePanelTimeMarkerAndRefresh(t *testing.T) {
	app := usageFixture()
	now := time.Now()
	app.usage.readings[0].Windows = []agentusage.Window{
		{Name: "5 小时", Used: 2, DurationMins: 300, ResetsAt: now.Add(207 * time.Minute).Unix(), Reset: now.Add(207 * time.Minute).Format("01-02 15:04")},
		{Name: "7 天", Used: 60, DurationMins: 10080, ResetsAt: now.Add(100 * time.Hour).Unix(), Reset: now.Add(100 * time.Hour).Format("01-02 15:04")},
	}
	tt := ui.NewTester(app.usagePanelView, usagePanelWidth, app.usage.height())
	if !tt.HasText("已用 2%") || !tt.HasText("均速 31% · 少用 29 个百分点") {
		t.Fatalf("missing pacing: %q", tt.Texts())
	}
	track, trackOK := tt.Find("额度进度 5 小时")
	marker, markerOK := tt.Find("时间进度 5 小时")
	if !trackOK || !markerOK || math.Abs(float64((marker.X+marker.W/2-track.X)/track.W)-0.31) > 0.01 || marker.Y < track.Y || marker.Y+marker.H > track.Y+track.H {
		t.Fatalf("time marker is misplaced: track=%v marker=%v", track, marker)
	}
	pixel := tt.Image().RGBAAt(int(marker.X+marker.W/2), int(marker.Y+marker.H/2))
	if pixel.R != desktopLight.ink.R || pixel.G != desktopLight.ink.G || pixel.B != desktopLight.ink.B {
		t.Fatalf("time marker did not paint over the track: marker=%v pixel=%v", marker, pixel)
	}
	assertUsageControls(t, tt, app.usage.height())
	saveUsageImage(t, tt, "usage-time-light")
	tt.SetDark(true)
	saveUsageImage(t, tt, "usage-time-dark")
	tt.Advance(30 * time.Minute)
	after, _ := tt.Find("时间进度 5 小时")
	if after.X <= marker.X+20 || !tt.HasText("均速 41% · 少用 39 个百分点") {
		t.Fatal("time progress did not advance while the panel stayed open")
	}
	app.usage.readings[0].Windows[0].ResetsAt = 0
	tt.Frame()
	if _, ok := tt.Find("时间进度 5 小时"); ok {
		t.Fatal("a display-only reset string produced a fictitious time marker")
	}
	for _, reset := range []time.Time{now.Add(7 * time.Hour), now.Add(-time.Hour)} {
		app.usage.readings[0].Windows[0].ResetsAt = reset.Unix()
		tt.Frame()
		marker, ok := tt.Find("时间进度 5 小时")
		if !ok || marker.X < track.X || marker.X+marker.W > track.X+track.W {
			t.Fatalf("time marker clips at a period boundary: track=%v marker=%v", track, marker)
		}
	}
}
