//go:build !windows

package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The fake turn ends only after a second user message, and succeeds only
// when that message is the steered one.
const steerFake = `#!/bin/sh
read -r l
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'
read -r first
read -r second
case "$second" in
*steered*) printf '%s\n' '{"type":"result","subtype":"success","is_error":false}' ;;
*) printf '%s\n' '{"type":"result","subtype":"error","is_error":true}' ;;
esac
`

func TestSteerWritesIntoTheRunningTurn(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(steerFake), 0o755); err != nil {
		t.Fatal(err)
	}
	result := make(chan bool, 1)
	done := make(chan error, 1)
	c, err := Start(context.Background(), Options{Binary: bin, Cwd: dir, PermissionMode: "manual", Content: []map[string]any{{"type": "text", "text": "first"}}},
		func(f Frame) {
			if f.Type == "result" {
				result <- !f.IsError
			}
		}, func(e error) { done <- e })
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Minute); !c.Steerable(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the turn never took more messages")
		}
	}
	if err := c.Steer("s", []map[string]any{{"type": "text", "text": "steered"}}); err != nil {
		t.Fatal(err)
	}
	if ok := <-result; !ok {
		t.Fatal("the steered message did not reach the turn")
	}
	<-done
	if err := c.Steer("s", nil); err != ErrNotSteerable {
		t.Fatalf("steer after the turn: %v", err)
	}
}
