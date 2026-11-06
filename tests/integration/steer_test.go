//go:build !windows

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/controller"
	"miya/internal/model"
)

// steerClaude records the second message of the turn, then ends it.
const steerClaude = `#!/bin/sh
read -r l
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'
read -r first
read -r second
printf '%s\n' "$second" > "$MIYA_OUT"
printf '%s\n' '{"type":"result","subtype":"success","is_error":false}'
`

func TestSteerFoldsIntoTheRunningClaudeTurn(t *testing.T) {
	c, d := queueController(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "second")
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(steerClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	c.ClaudeBinary, c.ClaudeEnv = bin, []string{"MIYA_OUT=" + out}
	c.SetThreads(nil)
	id := c.Submit(model.SendRequest{Agent: appmodel.AgentClaude, Text: "first"})
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		d.drain()
		if run := c.ClaudeRuns[id]; run != nil && run.Steerable() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never took more messages")
		}
	}
	if c.Submit(model.SendRequest{ThreadID: id, Text: "steered please", Steer: true}) != id {
		t.Fatal("steer refused")
	}
	th := c.ThreadWithID(id)
	if got := userTexts(th); len(got) != 2 || got[1] != "steered please" || len(th.Queue) != 0 {
		t.Fatalf("after steering: %q queue %+v", got, th.Queue)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if data, err := os.ReadFile(out); err == nil && strings.Contains(string(data), "steered please") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the turn never received the steered message")
		}
	}
}

func TestSteerFallsBackToTheQueue(t *testing.T) {
	c, _ := queueController(t)
	// No running process, as a turn still starting.
	if c.Submit(model.SendRequest{ThreadID: 1, Text: "later", Steer: true}) != 1 {
		t.Fatal("refused")
	}
	if th := c.ThreadWithID(1); len(th.Queue) != 1 || th.Queue[0].Steer || len(th.Messages) != 0 {
		t.Fatalf("not queued: %+v", th)
	}
	// Codex cannot steer a thread without a running turn either.
	if (controller.CodexBackend{}).Steer(c, model.Thread{ID: 1, ServerID: "t1", Resumed: true}, model.SendRequest{Text: "x"}) {
		t.Fatal("Codex steered without a turn")
	}
}
