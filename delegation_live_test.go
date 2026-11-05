//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"miya/internal/claude"
	"miya/internal/codex"
	dg "miya/internal/delegation"
)

// Opt-in real CLI verification. Only marker prompts in empty temporary
// workspaces are sent to the configured providers; no repository is attached.
func TestInstalledAgentsCollaboration(t *testing.T) {
	if os.Getenv("MIYA_COLLABORATION_LIVE_TEST") != "1" {
		t.Skip("set MIYA_COLLABORATION_LIVE_TEST=1 for real Codex/Claude collaboration")
	}
	command, err := claude.ParseCommand("")
	if err != nil {
		t.Fatal(err)
	}
	claudeConfig := claudeConfigDir()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	// Start before the fixture isolates Miya's HOME so the native app-server
	// retains its own configured account, as it does in the installed app.
	client, err := codex.Start(ctx, "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	f := newServiceFixture(t)
	var cwd string
	f.serv.Do(func() { cwd = f.srv.c.cwd })
	var parent codex.ThreadResponse
	if err := client.Call(ctx, "thread/start", map[string]any{"cwd": cwd, "sandbox": "workspace-write", "approvalPolicy": "on-request", "developerInstructions": "Transport verification in an empty workspace. Do not use tools or read files. When sent a collaboration notification, reply only MIYA_NOTICE_OK."}, &parent); err != nil {
		t.Fatal(err)
	}
	f.serv.Do(func() {
		c := f.srv.c
		c.client = client
		c.setReady(true)
		c.claudeBinary, c.claudeArgs = command.Path, append(command.Args, "--tools", "")
		c.claudeEnv = append(command.Env, "CLAUDE_CONFIG_DIR="+claudeConfig)
	})
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		for frame := range client.Events() {
			if len(frame.ID) > 0 {
				_ = client.Reject(frame.ID, "Marker verification does not permit tool requests")
				continue
			}
			f.srv.c.dispatch(func() { f.srv.c.handle(frame) })
		}
	}()
	t.Cleanup(func() { client.Close(); <-streamDone })
	caller := collaborationCaller(t, f, agentCodex, parent.Thread.ID)
	var from string
	f.serv.Do(func() {
		c := f.srv.c
		c.update(caller, func(t *Thread) { t.Busy = true; t.LaunchModel = parent.Model })
		from = c.threadRef(c.threadWithID(caller)).URL()
	})
	wait := func(condition func() bool, description string) {
		t.Helper()
		for !condition() {
			select {
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s", description)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	var ids []string
	for _, agent := range []string{agentCodex, agentClaude} {
		result, err := collaborationPhoneCall(t, f, "start_task", from, map[string]any{"request_id": "live-" + agent, "agent": agent, "task": "Reply only MIYA_TASK_OK. Do not use tools or read files.", "notify": "on_finish"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, result["task_id"].(string))
	}
	var bothBusy bool
	f.serv.Do(func() {
		busy := 0
		for _, thread := range f.srv.c.allThreads() {
			if thread.ExecutionID == ids[0] || thread.ExecutionID == ids[1] {
				if thread.Busy {
					busy++
				}
			}
		}
		bothBusy = busy == 2
	})
	if !bothBusy {
		t.Fatal("two independent native task sessions did not overlap")
	}
	for _, id := range ids {
		wait(func() bool { task, _ := f.srv.c.collaboration.Manager.Read(id); return dg.Terminal(task.State) }, "native task "+id)
		task, _ := f.srv.c.collaboration.Manager.Read(id)
		if task.State != dg.Completed || !strings.Contains(task.Result.Text, "MIYA_TASK_OK") || task.SessionEpoch == "" {
			t.Fatalf("live %s result: %+v", task.Agent, task)
		}
		if task.Agent == agentClaude {
			if proof, err := f.srv.c.collaboration.Manager.NativeOutcome(id); err != nil || proof.NativeResultID == "" {
				t.Fatal("live Claude root result did not yield recheck evidence", proof, err)
			}
		}
		t.Logf("%s completed in its own session; native binding and marker verified", task.Agent)
	}
	// Both results must coalesce while the parent is busy. Releasing that
	// barrier should submit exactly one real native notification turn.
	f.serv.Do(func() { f.srv.c.update(caller, func(t *Thread) { t.Busy = false }) })
	wait(func() bool {
		deliveries := f.srv.c.collaboration.Manager.View().Deliveries
		if len(deliveries) != 1 {
			return false
		}
		for _, d := range deliveries {
			return d.State == "delivered" && len(d.TaskIDs) == 2 && d.TargetTurnID != ""
		}
		return false
	}, "merged native completion notification")
	wait(func() bool {
		ready := false
		f.serv.Do(func() { ready = !f.srv.c.threadWithID(caller).Busy })
		return ready
	}, "parent notification reply")
	f.serv.Do(func() {
		notices := 0
		replied := false
		for _, message := range f.srv.c.threadWithID(caller).Messages {
			if message.Role == "user" && strings.HasPrefix(message.Content, "[Miya collaboration]") {
				notices++
			}
			if message.Role == "assistant" && strings.Contains(message.Content, "MIYA_NOTICE_OK") {
				replied = true
			}
		}
		if notices != 1 {
			t.Errorf("merged completion started %d parent turns", notices)
		}
		if !replied {
			t.Error("parent did not finish the real notification reply")
		}
	})
	t.Log("real Codex/Claude overlap and one merged native completion notification verified")
}

// The isolated native CLI launches this test executable as its stdio MCP
// bridge. Exit directly after EOF so the testing package never writes a PASS
// banner into the JSON-RPC stream.
func TestCollaborationMCPBridgeProcess(t *testing.T) {
	if os.Getenv("MIYA_COLLABORATION_MCP_HELPER") != "1" {
		t.Skip("MCP subprocess helper")
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" && i+2 < len(args) {
			os.Args = []string{args[0], "-mcp", args[i+1], args[i+2]}
			if err := runMCP(); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestInstalledAgentCollaborationMCP(t *testing.T) {
	if os.Getenv("MIYA_COLLABORATION_LIVE_TEST") != "1" {
		t.Skip("set MIYA_COLLABORATION_LIVE_TEST=1 for real agent MCP delegation")
	}
	command, err := claude.ParseCommand("")
	if err != nil {
		t.Fatal(err)
	}
	config := claudeConfigDir()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	client, err := codex.Start(ctx, "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	f := newServiceFixture(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var cwd string
	f.serv.Do(func() { cwd = f.srv.c.cwd })
	mcp := collaborationArgsJSON(map[string]any{"mcpServers": map[string]any{"miya": map[string]any{
		"type": "stdio", "command": exe,
		"args": []string{"-test.run=^TestCollaborationMCPBridgeProcess$", "--", cwd, f.sock},
		"env":  map[string]string{"MIYA_COLLABORATION_MCP_HELPER": "1"},
	}}})
	f.serv.Do(func() {
		c := f.srv.c
		c.client = client
		c.setReady(true)
		c.claudeBinary = command.Path
		c.claudeArgs = append(command.Args, "--tools", "", "--strict-mcp-config", "--mcp-config", string(mcp), "--allowedTools", "mcp__miya__read_task,mcp__miya__wait_task", "--disable-slash-commands", "--settings", `{"disableAllHooks":true}`, "--no-chrome")
		c.claudeEnv = append(command.Env, "CLAUDE_CONFIG_DIR="+config)
	})
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		for frame := range client.Events() {
			if len(frame.ID) > 0 {
				_ = client.Reject(frame.ID, "Marker verification does not permit native tool requests")
				continue
			}
			f.srv.c.dispatch(func() { f.srv.c.handle(frame) })
		}
	}()
	t.Cleanup(func() { client.Close(); <-streamDone })
	var parentID int
	prompt := "Miya MCP continuation verification in an empty temporary workspace. Use start_task with request_id miya-live-mcp-1, agent codex, task exactly 'Reply only MIYA_MCP_TASK_OK. Do not use tools or read files.', context none. Omit the notify argument to use automatic continuation. After start_task immediately end your first reply with MIYA_WAITING; do not wait or read the task in this first turn. When Miya later sends its completion notification, use read_task to verify the fixed result and then reply only MIYA_MCP_PARENT_OK. Use only start_task and read_task; do not access files, use built-in tools, or create any other tasks."
	f.serv.Do(func() {
		parentID = f.srv.c.submit(SendRequest{Agent: agentClaude, Project: cwd, Text: prompt, MemoryControl: true})
	})
	if parentID == 0 {
		t.Fatal("native parent did not start")
	}
	observer, err := dialService(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	approved := false
	var parent Thread
	for {
		var approvals []codex.Approval
		f.serv.Do(func() { parent = f.srv.c.threadWithID(parentID); approvals = append(approvals, f.srv.c.approvals()...) })
		for _, approval := range approvals {
			if approval.ThreadID != parent.ServerID {
				t.Fatal("approval routed to another native session")
			}
			switch approval.Command {
			case "mcp__miya__start_task":
				var args collaborationArgs
				if json.Unmarshal(approval.Payload, &args) != nil || args.Agent != agentCodex || args.RequestID != "miya-live-mcp-1" || args.Task != "Reply only MIYA_MCP_TASK_OK. Do not use tools or read files." || args.Notify == "none" {
					t.Fatal("refusing unexpected live task parameters", string(approval.Payload))
				}
			case "mcp__miya__wait_task", "mcp__miya__read_task":
			default:
				t.Fatal("refusing unexpected native tool", approval.Command)
			}
			if err := observer.call("respond", map[string]any{"id": approval.ID, "result": map[string]any{"decision": "accept"}}, nil); err != nil {
				t.Fatal(err)
			}
			approved = true
		}
		if !parent.Busy && strings.Contains(managedResult(parent.Messages, false).Text, "MIYA_MCP_PARENT_OK") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native MCP workflow timed out", parent.Status)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !strings.Contains(managedResult(parent.Messages, false).Text, "MIYA_MCP_PARENT_OK") {
		t.Fatal("native parent did not verify its MCP task result", parent.Status, managedResult(parent.Messages, false))
	}
	tasks := f.srv.c.collaboration.Manager.View().Tasks
	if len(tasks) != 1 {
		t.Fatalf("native MCP workflow created %d tasks", len(tasks))
	}
	for _, task := range tasks {
		if task.State != dg.Completed || task.Agent != agentCodex || task.From != (ThreadRef{Source: testReferenceSource, Backend: agentClaude, SessionID: parent.ServerID}).URL() || task.NativeTurnID == "" || !strings.Contains(task.Result.Text, "MIYA_MCP_TASK_OK") {
			t.Fatal("native tool caller or fixed result was not verified", task)
		}
	}
	for _, tool := range []string{"start_task", "read_task"} {
		found := false
		for _, message := range parent.Messages {
			if strings.HasPrefix(message.Content, "mcp__miya__"+tool+"\n") {
				found = true
			}
		}
		if !found {
			t.Fatal("native parent did not use required MCP tool", tool)
		}
	}
	initialReply, notifications := false, 0
	for _, message := range parent.Messages {
		if message.Role == "assistant" && strings.Contains(message.Content, "MIYA_WAITING") {
			initialReply = true
		}
		if message.Role == "user" && strings.HasPrefix(message.Content, "[Miya collaboration]") {
			notifications++
		}
	}
	if !initialReply || notifications != 1 {
		t.Fatalf("missing initial reply or duplicate/missing continuation: waiting=%v notifications=%d", initialReply, notifications)
	}
	t.Logf("real Claude → MCP → Codex task → automatic second parent turn → read fixed result verified; native approval exercised=%v", approved)
}
