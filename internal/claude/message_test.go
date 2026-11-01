package claude

import (
	"encoding/json"
	"testing"
)

func TestFrameMessageShapes(t *testing.T) {
	cases := []struct {
		name, line, id, text string
		blocks               int
	}{
		{"string message", `{"type":"system","message":"hello"}`, "", "hello", 1},
		{"empty string message", `{"type":"system","message":""}`, "", "", 0},
		{"null message", `{"type":"system","message":null}`, "", "", 0},
		{"string content", `{"type":"user","message":{"role":"user","content":"hi"}}`, "", "hi", 1},
		{"block content", `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"ok"}]}}`, "m1", "ok", 1},
	}
	for _, c := range cases {
		var f Frame
		if err := json.Unmarshal([]byte(c.line), &f); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if f.Message.ID != c.id || len(f.Message.Content) != c.blocks {
			t.Fatalf("%s: got %+v", c.name, f.Message)
		}
		if c.blocks > 0 && (f.Message.Content[0].Type != "text" || f.Message.Content[0].Text != c.text) {
			t.Fatalf("%s: got %+v", c.name, f.Message.Content[0])
		}
	}
}
