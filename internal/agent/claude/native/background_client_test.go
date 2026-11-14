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

const backgroundShellFake = `#!/bin/sh
read -r line
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'
read -r line
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"bash","name":"Bash","input":{"command":"node app.js","run_in_background":true}}]}}'
printf '%s\n' '{"type":"system","subtype":"task_started","task_id":"1234","tool_use_id":"bash","task_type":"local_bash"}'
printf '%s\n' '{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"1234","task_type":"local_bash","description":"Web app"}]}'
printf '%s\n' '{"type":"result","result":"Started"}'
read -r line || exit 41
case "$line" in *probe*) ;; *) exit 42 ;; esac
printf '%s\n' '{"type":"system","subtype":"task_updated","task_id":"1234","patch":{"status":"completed"}}'
printf '%s\n' '{"type":"system","subtype":"background_tasks_changed","tasks":[]}'
read -r line && exit 43
exit 0
`

func TestBackgroundShellSurvivesReplyUntilNativeCompletion(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "claude")
	if err := os.WriteFile(binary, []byte(backgroundShellFake), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	frames := make(chan Frame, 20)
	done := make(chan error, 1)
	client, err := Start(ctx, Options{Binary: binary, Cwd: dir, SessionID: "shell-session"}, func(f Frame) { frames <- f }, func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	reply := false
	for {
		select {
		case f := <-frames:
			if f.Type == "result" {
				reply = true
				if !f.KeepingAlive || f.WaitingForAgents {
					t.Fatal("background shell ended or kept model turn busy")
				}
				if len(f.BackgroundTasks) != 1 || f.BackgroundTasks[0].Description != "Web app" || f.BackgroundTasks[0].Status != "running" {
					t.Fatalf("reply lost live background work: %+v", f.BackgroundTasks)
				}
				tasks, known, pid, epoch := client.Runtime()
				if len(tasks) != 1 || !known || pid <= 0 || epoch == "" || tasks[0].ID != "1234" {
					t.Fatal("missing native work and dedicated process identity")
				}
				if err := client.Write(map[string]any{"probe": true}); err != nil {
					t.Fatal(err)
				}
			}
			if f.Type == "system" && f.Subtype == "background_tasks_changed" && len(f.Tasks) == 0 && (f.BackgroundTasks == nil || len(f.BackgroundTasks) != 0) {
				t.Fatalf("completion did not publish an empty live snapshot: %+v", f.BackgroundTasks)
			}
		case err := <-done:
			if err != nil || !reply {
				t.Fatalf("shell lifetime failed: %v, reply=%v", err, reply)
			}
			return
		case <-ctx.Done():
			t.Fatal("shell did not end after native completion")
		}
	}
}

func TestBackgroundCompletionAllowsNativeFollowupBeforeReleasingCLI(t *testing.T) {
	script := strings.Replace(backgroundShellFake, "read -r line && exit 43", `printf '%s\n' '{"type":"command_lifecycle","state":"started","wakeup_source":"background_task"}'
printf '%s\n' '{"type":"assistant","message":{"id":"wake","content":[{"type":"text","text":"Finishing background work"}]}}'
read -r line || exit 44
case "$line" in *probeWake*) ;; *) exit 45 ;; esac
printf '%s\n' '{"type":"result","result":"Background work finished"}'
read -r line && exit 46`, 1)
	dir := t.TempDir()
	binary := filepath.Join(dir, "claude")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	frames := make(chan Frame, 20)
	done := make(chan error, 1)
	client, err := Start(ctx, Options{Binary: binary, Cwd: dir}, func(f Frame) { frames <- f }, func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	followup := false
	for {
		select {
		case f := <-frames:
			if f.Type == "result" {
				results++
				if results == 1 {
					if err := client.Write(map[string]any{"probe": true}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if f.Type == "assistant" && f.Message.ID == "wake" {
				followup = true
				if err := client.Write(map[string]any{"probeWake": true}); err != nil {
					t.Fatal("native followup lost its execution connection", err)
				}
			}
		case err := <-done:
			if err != nil || results != 2 || !followup {
				t.Fatalf("followup failed: %v results=%d followup=%v", err, results, followup)
			}
			return
		case <-ctx.Done():
			t.Fatal("native followup did not finish")
		}
	}
}
