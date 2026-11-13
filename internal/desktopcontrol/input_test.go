package desktopcontrol

import (
	"math"
	"testing"
)

// Only validation: these tests never post an event to the desktop.
func TestInputValidate(t *testing.T) {
	for _, c := range []struct {
		name string
		in   Input
		ok   bool
	}{
		{"click", Input{Kind: Click, X: 10, Y: 20}, true},
		{"negative points", Input{Kind: Move, X: -495, Y: -1440}, true},
		{"key with modifiers", Input{Kind: Key, KeyCode: 8, Modifiers: Command | Shift}, true},
		{"text", Input{Kind: Text, Text: "你好 👋"}, true},
		{"no kind", Input{}, false},
		{"unknown kind", Input{Kind: 99}, false},
		{"NaN", Input{Kind: Click, X: math.NaN()}, false},
		{"infinite target", Input{Kind: Drag, ToX: math.Inf(1)}, false},
		{"modifier on click", Input{Kind: Click, Modifiers: Command}, false},
		{"unknown modifier", Input{Kind: Key, Modifiers: 1}, false},
		{"empty text", Input{Kind: Text}, false},
		{"invalid UTF-8", Input{Kind: Text, Text: "\xff"}, false},
		{"modifier on text", Input{Kind: Text, Text: "a", Modifiers: Shift}, false},
	} {
		if err := c.in.validate(); (err == nil) != c.ok {
			t.Errorf("%s: validate() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
	if err := PostToProcess(0, Input{Kind: Key}); err == nil {
		t.Error("PostToProcess accepted pid 0")
	}
	if err := Post(Input{Kind: Click, X: math.NaN()}); err == nil {
		t.Error("Post accepted NaN")
	}
}
