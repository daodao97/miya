package integration

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
)

// The working label is still: an animated one kept the whole window
// building frames while a conversation ran. Its elapsed time still counts.
func TestWorkingLabelCounts(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "s1", Title: "Run", Busy: true, HistoryLoaded: true,
		StartedAtMs: time.Now().Add(-3 * time.Second).UnixMilli(), LastOutputAtMs: time.Now().UnixMilli()}})
	c.SetActive(1)
	label := func() string {
		for _, s := range tt.Texts() {
			if strings.Contains(s, "Working for ") {
				return s
			}
		}
		return ""
	}
	tt.Frame()
	before := label()
	if before == "" {
		t.Fatalf("no working label with its spaces in %q", tt.Texts())
	}
	time.Sleep(1100 * time.Millisecond) // the label reads the wall clock
	tt.Advance(1100 * time.Millisecond)
	tt.Frame()
	if after := label(); after == before {
		t.Fatalf("the elapsed time did not count: %q then %q", before, after)
	}
}

// TestWorkingLabelSnapshot writes frames of the label to look at:
// MIYA_SNAPSHOT=dir go test ./tests/integration -run TestWorkingLabelSnapshot
func TestWorkingLabelSnapshot(t *testing.T) {
	dir := os.Getenv("MIYA_SNAPSHOT")
	if dir == "" {
		t.Skip("set MIYA_SNAPSHOT")
	}
	app, tt := testApp(t)
	tt.SetScale(2)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "s1", Title: "Run", Busy: true, HistoryLoaded: true,
		StartedAtMs: time.Now().Add(-35 * time.Second).UnixMilli(), LastOutputAtMs: time.Now().UnixMilli()}})
	c.SetActive(1)
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
