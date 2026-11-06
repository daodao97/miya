//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	mcpserver "miya/internal/mcp"

	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
	"miya/internal/model"
	"miya/internal/svcproto"
	"miya/internal/widgets"
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
	claudeConfig := transcript.ConfigDir()
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
	f.serv.Do(func() { cwd = f.srv.C.Cwd })
	var parent codex.ThreadResponse
	if err := client.Call(ctx, "thread/start", map[string]any{"cwd": cwd, "sandbox": "workspace-write", "approvalPolicy": "on-request", "developerInstructions": "Transport verification in an empty workspace. Do not use tools or read files. When sent a collaboration notification, reply only MIYA_NOTICE_OK."}, &parent); err != nil {
		t.Fatal(err)
	}
	f.serv.Do(func() {
		c := f.srv.C
		c.Client = client
		c.SetReady(true)
		c.ClaudeBinary, c.ClaudeArgs = command.Path, append(command.Args, "--tools", "")
		c.ClaudeEnv = append(command.Env, "CLAUDE_CONFIG_DIR="+claudeConfig)
	})
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		for frame := range client.Events() {
			if len(frame.ID) > 0 {
				_ = client.Reject(frame.ID, "Marker verification does not permit tool requests")
				continue
			}
			f.srv.C.Dispatch(func() { f.srv.C.Handle(frame) })
		}
	}()
	t.Cleanup(func() { client.Close(); <-streamDone })
	caller := collaborationCaller(t, f, appmodel.AgentCodex, parent.Thread.ID)
	var from string
	f.serv.Do(func() {
		c := f.srv.C
		c.Update(caller, func(t *model.Thread) { t.Busy = true; t.LaunchModel = parent.Model })
		from = c.ThreadRef(c.ThreadWithID(caller)).URL()
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
	for _, agent := range []string{appmodel.AgentCodex, appmodel.AgentClaude} {
		result, err := collaborationPhoneCall(t, f, "start_task", from, map[string]any{"request_id": "live-" + agent, "agent": agent, "task": "Reply only MIYA_TASK_OK. Do not use tools or read files.", "notify": "on_finish"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, result["task_id"].(string))
	}
	var bothBusy bool
	f.serv.Do(func() {
		busy := 0
		for _, thread := range f.srv.C.AllThreads() {
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
		wait(func() bool {
			task, _ := f.srv.C.Collaboration.Manager.Read(id)
			return dg.Terminal(task.State)
		}, "native task "+id)
		task, _ := f.srv.C.Collaboration.Manager.Read(id)
		if task.State != dg.Completed || !strings.Contains(task.Result.Text, "MIYA_TASK_OK") || task.SessionEpoch == "" {
			t.Fatalf("live %s result: %+v", task.Agent, task)
		}
		if task.Agent == appmodel.AgentClaude {
			if proof, err := f.srv.C.Collaboration.Manager.NativeOutcome(id); err != nil || proof.NativeResultID == "" {
				t.Fatal("live Claude root result did not yield recheck evidence", proof, err)
			}
		}
		t.Logf("%s completed in its own session; native binding and marker verified", task.Agent)
	}
	// Both results must coalesce while the parent is busy. Releasing that
	// barrier should submit exactly one real native notification turn.
	f.serv.Do(func() { f.srv.C.Update(caller, func(t *model.Thread) { t.Busy = false }) })
	wait(func() bool {
		deliveries := f.srv.C.Collaboration.Manager.View().Deliveries
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
		f.serv.Do(func() { ready = !f.srv.C.ThreadWithID(caller).Busy })
		return ready
	}, "parent notification reply")
	f.serv.Do(func() {
		notices := 0
		replied := false
		for _, message := range f.srv.C.ThreadWithID(caller).Messages {
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
			if err := mcpserver.Run(); err != nil {
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
	config := transcript.ConfigDir()
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
	f.serv.Do(func() { cwd = f.srv.C.Cwd })
	mcp := widgets.CollaborationArgsJSON(map[string]any{"mcpServers": map[string]any{"miya": map[string]any{
		"type": "stdio", "command": exe,
		"args": []string{"-test.run=^TestCollaborationMCPBridgeProcess$", "--", cwd, f.sock},
		"env":  map[string]string{"MIYA_COLLABORATION_MCP_HELPER": "1"},
	}}})
	f.serv.Do(func() {
		c := f.srv.C
		c.Client = client
		c.SetReady(true)
		c.ClaudeBinary = command.Path
		c.ClaudeArgs = append(command.Args, "--tools", "", "--strict-mcp-config", "--mcp-config", string(mcp), "--allowedTools", "mcp__miya__read_task,mcp__miya__wait_task", "--disable-slash-commands", "--settings", `{"disableAllHooks":true}`, "--no-chrome")
		c.ClaudeEnv = append(command.Env, "CLAUDE_CONFIG_DIR="+config)
	})
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		for frame := range client.Events() {
			if len(frame.ID) > 0 {
				_ = client.Reject(frame.ID, "Marker verification does not permit native tool requests")
				continue
			}
			f.srv.C.Dispatch(func() { f.srv.C.Handle(frame) })
		}
	}()
	t.Cleanup(func() { client.Close(); <-streamDone })
	var parentID int
	prompt := "Miya MCP continuation verification in an empty temporary workspace. Use start_task with request_id miya-live-mcp-1, agent codex, task exactly 'Reply only MIYA_MCP_TASK_OK. Do not use tools or read files.', context none. Omit the notify argument to use automatic continuation. After start_task immediately end your first reply with MIYA_WAITING; do not wait or read the task in this first turn. When Miya later sends its completion notification, use read_task to verify the fixed result and then reply only MIYA_MCP_PARENT_OK. Use only start_task and read_task; do not access files, use built-in tools, or create any other tasks."
	f.serv.Do(func() {
		parentID = f.srv.C.Submit(model.SendRequest{Agent: appmodel.AgentClaude, Project: cwd, Text: prompt, MemoryControl: true})
	})
	if parentID == 0 {
		t.Fatal("native parent did not start")
	}
	observer, err := svcproto.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	approved := false
	var parent model.Thread
	for {
		var approvals []codex.Approval
		f.serv.Do(func() {
			parent = f.srv.C.ThreadWithID(parentID)
			approvals = append(approvals, f.srv.C.Approvals()...)
		})
		for _, approval := range approvals {
			if approval.ThreadID != parent.ServerID {
				t.Fatal("approval routed to another native session")
			}
			switch approval.Command {
			case "mcp__miya__start_task":
				var args coordinator.Args
				if json.Unmarshal(approval.Payload, &args) != nil || args.Agent != appmodel.AgentCodex || args.RequestID != "miya-live-mcp-1" || args.Task != "Reply only MIYA_MCP_TASK_OK. Do not use tools or read files." || args.Notify == "none" {
					t.Fatal("refusing unexpected live task parameters", string(approval.Payload))
				}
			case "mcp__miya__wait_task", "mcp__miya__read_task":
			default:
				t.Fatal("refusing unexpected native tool", approval.Command)
			}
			if err := observer.Call("respond", map[string]any{"id": approval.ID, "result": map[string]any{"decision": "accept"}}, nil); err != nil {
				t.Fatal(err)
			}
			approved = true
		}
		if !parent.Busy && strings.Contains(coordinator.NativeResult(parent.Messages, false).Text, "MIYA_MCP_PARENT_OK") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native MCP workflow timed out", parent.Status)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !strings.Contains(coordinator.NativeResult(parent.Messages, false).Text, "MIYA_MCP_PARENT_OK") {
		t.Fatal("native parent did not verify its MCP task result", parent.Status, coordinator.NativeResult(parent.Messages, false))
	}
	tasks := f.srv.C.Collaboration.Manager.View().Tasks
	if len(tasks) != 1 {
		t.Fatalf("native MCP workflow created %d tasks", len(tasks))
	}
	for _, task := range tasks {
		if task.State != dg.Completed || task.Agent != appmodel.AgentCodex || task.From != (model.ThreadRef{Source: testReferenceSource, Backend: appmodel.AgentClaude, SessionID: parent.ServerID}).URL() || task.NativeTurnID == "" || !strings.Contains(task.Result.Text, "MIYA_MCP_TASK_OK") {
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
