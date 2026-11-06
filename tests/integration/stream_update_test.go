package integration

import (
	"encoding/json"
	"testing"

	"miya/internal/agent/claude/native"
	"miya/internal/model"
	"miya/internal/threadlist"
)

// Streamed text must not write the sidebar's cache on the UI thread for
// every delta, which made replies show in bursts.
func TestStreamingDeltasDoNotRewriteThreadCache(t *testing.T) {
	writes := 0
	old := threadlist.Persist
	threadlist.Persist = func(map[string][]model.Thread) { writes++ }
	t.Cleanup(func() { threadlist.Persist = old })

	c := testController()
	c.Cwd = "/work/demo"
	c.ProjectCache = map[string][]model.Thread{}
	c.Handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
	writes = 0
	for i := 0; i < 100; i++ {
		c.Handle(event("item/agentMessage/delta", `{"threadId":"t1","turnId":"turn1","itemId":"a","delta":"word "}`))
	}
	if got := len(c.Threads()[0].Messages[0].Content); got != 500 {
		t.Fatalf("deltas lost: %d bytes", got)
	}
	if writes != 0 {
		t.Fatalf("Codex deltas wrote the cache %d times", writes)
	}

	c.SetThreads(append(c.Threads(), model.Thread{ID: 3, Agent: "claude-code", ServerID: "s3"}))
	emit := func(s string) {
		var f claude.Frame
		if err := json.Unmarshal([]byte(s), &f); err != nil {
			t.Fatal(err)
		}
		c.HandleClaude(3, f)
	}
	emit(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`)
	writes = 0
	for i := 0; i < 100; i++ {
		emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"word "}}}`)
	}
	if writes != 0 {
		t.Fatalf("Claude deltas wrote the cache %d times", writes)
	}

	c.Handle(event("thread/name/updated", `{"threadId":"t1","name":"Renamed"}`))
	if writes == 0 {
		t.Fatal("a new title was not saved")
	}
}
