//go:build !windows

package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The native protocol emits a second turn without another user input; the
// third turn reads a manual message on the same stdin and cancels the job.
const scheduledFake = `#!/bin/sh
read -r line
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'
read -r line
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"create","name":"CronCreate","input":{"cron":"* * * * *","prompt":"check"}}]}}'
printf '%s\n' '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"create"}]},"tool_use_result":{"id":"12345678","recurring":true,"nextFireAt":1}}'
printf '%s\n' '{"type":"result","is_error":false}'
sleep 0.1
printf '%s\n' '{"type":"command_lifecycle","state":"started","wakeup_source":"schedule_wakeup"}'
printf '%s\n' '{"type":"result","is_error":false}'
read -r line
case "$line" in *manual*) ;; *) exit 30 ;; esac
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"cancel","name":"CronDelete","input":{"id":"12345678"}}]}}'
printf '%s\n' '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"cancel"}]},"tool_use_result":{"id":"12345678"}}'
printf '%s\n' '{"type":"result","is_error":false}'
read -r line
`

func TestScheduledSessionSurvivesResultAndAcceptsManualTurn(t *testing.T) {
	testScheduledSession(t, false)
}

func TestScheduledSessionAppliesControlsBeforeManualTurn(t *testing.T) {
	testScheduledSession(t, true)
}

func testScheduledSession(t *testing.T, configure bool) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := scheduledFake
	if configure {
		controls := `read -r line
case "$line" in *set_permission_mode*) ;; *) exit 31 ;; esac
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"settings-1"}}'
read -r line
case "$line" in *set_model*) ;; *) exit 32 ;; esac
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"settings-2"}}'
`
		script = strings.Replace(script, "read -r line\ncase \"$line\" in *manual*", controls+"read -r line\ncase \"$line\" in *manual*", 1)
	}
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	frames, done := make(chan Frame, 32), make(chan error, 1)
	c, err := Start(ctx, Options{Binary: bin, Cwd: dir, Content: []map[string]any{{"type": "text", "text": "/loop 1m check"}}}, func(f Frame) { frames <- f }, func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	results, automatic := 0, false
	for {
		select {
		case f := <-frames:
			if f.Type == "command_lifecycle" {
				automatic = true
			}
			if f.Type != "result" {
				continue
			}
			results++
			if results < 3 && !f.KeepingAlive {
				t.Fatal("scheduled session closed at result")
			}
			if results == 2 {
				if c.Steerable() {
					t.Fatal("waiting scheduler advertised a running turn")
				}
				content := []map[string]any{{"type": "text", "text": "manual"}}
				var err error
				if configure {
					err = c.SendWithOptions(ctx, Options{SessionID: "s", Content: content, Model: "next-model", PermissionMode: "default"})
				} else {
					err = c.Send("s", content)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if results == 3 && (f.KeepingAlive || f.Schedules[0].Status != "stopped") {
				t.Fatal("cancellation retained the session")
			}
		case err := <-done:
			for len(frames) > 0 {
				f := <-frames
				if f.Type == "result" {
					results++
					if f.KeepingAlive {
						t.Fatal("final result retained canceled job")
					}
				}
			}
			if err != nil || results != 3 || !automatic {
				t.Fatalf("results=%d automatic=%v error=%v", results, automatic, err)
			}
			return
		case <-ctx.Done():
			t.Fatal("scheduled session stalled")
		}
	}
}
