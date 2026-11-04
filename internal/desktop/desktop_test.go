package desktop

import (
	"math"
	"testing"
)

func TestInputValidation(t *testing.T) {
	for _, in := range []Input{{Kind: "click", X: .3, Y: .7}, {Kind: "drag", ToX: 1, ToY: 1}, {Kind: "text", Text: "中文 🐈 password"}, {Kind: "key", Key: "l", Modifier: "command"}, {Kind: "scroll", DeltaY: -1000}} {
		if err := in.Validate(); err != nil {
			t.Fatalf("valid input rejected: %+v: %v", in, err)
		}
	}
	for _, in := range []Input{{Kind: "launch"}, {Kind: "click", X: math.NaN()}, {Kind: "click", Y: math.Inf(1)}, {Kind: "drag", ToX: 1.1}, {Kind: "key", Key: "unknown"}, {Kind: "key", Key: "a", Modifier: "arbitrary"}, {Kind: "scroll", DeltaY: 1001}, {Kind: "text"}, {Kind: "text", Text: string([]byte{0xff})}} {
		if err := in.Validate(); err == nil {
			t.Fatalf("invalid input accepted: %+v", in)
		}
	}
}

func TestInputActionUsesNativeSemanticsAndBounds(t *testing.T) {
	d := Dialog{Actions: []Action{
		{ID: "primary", Label: "任意文案", Default: true, Enabled: true, X: .5, Y: .6, Width: .3, Height: .1},
		{ID: "cancel", Label: "允许访问", Cancel: true, Enabled: true, X: .1, Y: .6, Width: .3, Height: .1},
	}}
	for _, tc := range []struct {
		in Input
		id string
	}{
		{Input{Kind: "key", Key: "enter"}, "primary"},
		{Input{Kind: "key", Key: "escape"}, "cancel"},
		{Input{Kind: "click", X: .65, Y: .65}, "primary"},
		{Input{Kind: "click", X: .25, Y: .65}, "cancel"},
		{Input{Kind: "key", Key: "enter", Modifier: "command"}, ""},
		{Input{Kind: "click", X: .9, Y: .9}, ""},
	} {
		a, ok := d.InputAction(tc.in)
		if ok != (tc.id != "") || a.ID != tc.id {
			t.Fatalf("%+v: %q %v", tc.in, a.ID, ok)
		}
	}
	d.Actions[0].Enabled = false
	if _, ok := d.InputAction(Input{Kind: "key", Key: "enter"}); ok {
		t.Fatal("disabled default button matched")
	}
}
