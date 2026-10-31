package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkingLabelWaves(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "s1", Title: "Run", Busy: true, HistoryLoaded: true,
		StartedAtMs: time.Now().Add(-3 * time.Second).UnixMilli(), LastOutputAtMs: time.Now().UnixMilli()}})
	c.setActive(1)
	tt.Frame()
	found := false
	for _, s := range tt.Texts() {
		found = found || strings.Contains(s, "Working for ")
	}
	if !found {
		t.Fatalf("no working label with its spaces in %q", tt.Texts())
	}
	before := tt.Image()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	after := tt.Image()
	if !tt.FramePending() && string(before.Pix) == string(after.Pix) {
		t.Fatal("the label does not animate")
	}
}

// TestWorkingLabelSnapshot writes frames of the label to look at:
// MIYA_SNAPSHOT=dir go test -run TestWorkingLabelSnapshot
func TestWorkingLabelSnapshot(t *testing.T) {
	dir := os.Getenv("MIYA_SNAPSHOT")
	if dir == "" {
		t.Skip("set MIYA_SNAPSHOT")
	}
	app, tt := testApp(t)
	tt.SetScale(2)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "s1", Title: "Run", Busy: true, HistoryLoaded: true,
		StartedAtMs: time.Now().Add(-35 * time.Second).UnixMilli(), LastOutputAtMs: time.Now().UnixMilli()}})
	c.setActive(1)
	for i := 0; i < 3; i++ {
		tt.Advance(500 * time.Millisecond)
		tt.Frame()
		img := tt.Image()
		r, _ := tt.Find("Run")
		_ = r
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("wave-%d.png", i)))
		if err != nil {
			t.Fatal(err)
		}
		// The label sits at the bottom of the conversation, above the composer.
		b := img.Bounds()
		sub := img.SubImage(image.Rect(b.Min.X, b.Max.Y*55/100, b.Max.X*70/100, b.Max.Y*85/100))
		if err := png.Encode(f, sub); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
