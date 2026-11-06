package integration

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/controller"
	"miya/internal/conversation"
	mcpserver "miya/internal/mcp"
	mcpconfig "miya/internal/mcp/config"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/model"
	"miya/internal/subagent"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
)

// mcpExchange runs the MCP server over the given request lines and returns
// its responses by id.
func mcpExchange(t *testing.T, call func(op string, args, out any) error, lines ...string) map[string]map[string]any {
	t.Helper()
	var out strings.Builder
	if err := mcpserver.Serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, "/ws", call, func(time.Duration) {}); err != nil {
		t.Fatal(err)
	}
	res := map[string]map[string]any{}
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad response %q: %v", sc.Text(), err)
		}
		id, _ := json.Marshal(m["id"])
		res[string(id)] = m
	}
	return res
}

func mcpText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	content := r["content"].([]any)
	return content[0].(map[string]any)["text"].(string), r["isError"] == true
}

// assign copies v into out, as the service client decodes a reply.
func assign(out, v any) {
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, out)
}

func TestMCPServerHandshakeAndTools(t *testing.T) {
	res := mcpExchange(t, nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"p","method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope"}`,
		`not json`,
	)
	init := res["1"]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["serverInfo"].(map[string]any)["name"] != "miya" {
		t.Fatalf("bad initialize: %v", init)
	}
	var names []string
	for _, tool := range res["2"]["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "request_desktop_control,runtime_info,list_threads,read_thread,thread_status,delegate,list_automations,create_automation,update_automation,report_result,list_agents,start_task,read_task,wait_task,send_thread,cancel_task,resolve_task,watch_task,unwatch_task,list_tasks,search_memory,remember,forget_memory" {
		t.Fatalf("bad tools: %v", names)
	}
	if res[`"p"`]["result"] == nil || res["3"]["error"].(map[string]any)["code"] != float64(-32601) || res["null"]["error"] == nil {
		t.Fatalf("bad ping, unknown method or parse error: %v", res)
	}
	if len(res) != 5 {
		t.Fatalf("a notification was answered: %v", res)
	}
}

func TestMCPDelegateRetriesThenWaitsForTheReply(t *testing.T) {
	var ops []string
	unknown, statuses := 1, []string{subagent.Running, subagent.Running, subagent.Completed}
	call := func(op string, args, out any) error {
		ops = append(ops, op)
		r := args.(mcpproto.Request)
		switch op {
		case "mcpDelegate":
			if unknown > 0 {
				unknown--
				return errors.New(mcpproto.CallerUnknown)
			}
			if r.Task != "写测试" || r.Agent != appmodel.AgentCodex || !r.NoContext || r.Workspace != "/ws" || r.Tool != "delegate" {
				t.Errorf("bad delegation: %+v", r)
			}
			assign(out, mcpproto.DelegateResult{Thread: 7, Agent: appmodel.AgentCodex})
		case "mcpStatus":
			status := statuses[0]
			statuses = statuses[1:]
			assign(out, mcpproto.StatusResult{ID: r.Thread, Title: "测试", Agent: appmodel.AgentCodex, Status: status, Reply: "测试都加好了"})
		}
		return nil
	}
	res := mcpExchange(t, call, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delegate","arguments":{"task":"写测试","agent":"codex","include_context":false}}}`)
	text, isErr := mcpText(t, res["1"])
	if isErr || !strings.Contains(text, "Handed off to session Creating (Codex)") || !strings.Contains(text, "Completed") || !strings.Contains(text, "测试都加好了") {
		t.Fatalf("bad delegate result: %q", text)
	}
	if strings.Join(ops, ",") != "mcpDelegate,mcpDelegate,mcpStatus,mcpStatus,mcpStatus" {
		t.Fatalf("bad calls: %v", ops)
	}
}

func TestMCPToolErrorsAreToolResults(t *testing.T) {
	call := func(op string, args, out any) error {
		return errors.New("Miya background service is not running. Open Miya first.")
	}
	res := mcpExchange(t, call, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_threads","arguments":{}}}`)
	if text, isErr := mcpText(t, res["1"]); !isErr || !strings.Contains(text, "background service is not running") {
		t.Fatalf("error not reported to the agent: %q %v", text, isErr)
	}
}

func mcpHost(t *testing.T) *controller.Controller {
	c := testController()
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Agent, c.SetAgent = uiruntime.CreateSignal(appmodel.AgentCodex)
	src := handoffSource()
	src.ID = 1
	src.Busy = true
	src.Messages = append(src.Messages[:4:4], model.Message{ID: 9, Role: "tool", Kind: "claudeTool", Status: "inProgress", Content: "mcp__miya__delegate\n{\"task\":\"写测试\"}"})
	c.SetThreads([]model.Thread{src, {ID: 2, Agent: appmodel.AgentCodex, ServerID: "x", Title: "测试", Busy: true, HistoryLoaded: true}})
	return c
}

func TestMCPDelegateHandsWorkWithTheCallersContext(t *testing.T) {
	c := mcpHost(t)
	if caller, ok := c.McpCaller("delegate"); !ok || caller.ID != 1 {
		t.Fatalf("caller not found: %+v", caller)
	}
	res, err := c.McpHandle("mcpDelegate", mcpproto.Request{Tool: "delegate", Task: "给登录补测试", Thread: 2})
	if err != nil || res.(mcpproto.DelegateResult).Thread != 2 {
		t.Fatalf("delegation failed: %v %v", res, err)
	}
	target := c.ThreadWithID(2)
	if len(target.Queue) != 1 || !strings.HasPrefix(target.Queue[0].Text, "给登录补测试") || !strings.Contains(target.Queue[0].Text, model.ContextEnvelopeOpen) {
		t.Fatalf("task not sent with the caller's context: %+v", target.Queue)
	}
	source := c.ThreadWithID(1)
	if last := source.Messages[len(source.Messages)-1]; last.Kind != "handoff" || last.Agents[0].Name != "给登录补测试" {
		t.Fatalf("caller does not follow the work: %+v", last)
	}
	// The receiving conversation cannot hand work back up.
	c.Update(2, func(th *model.Thread) {
		th.Messages = append(th.Messages, model.Message{ID: c.NextID(), Role: "tool", Kind: "mcpToolCall", Status: "inProgress", Item: &codex.Item{Server: "miya", Tool: "delegate"}})
	})
	c.Update(1, func(th *model.Thread) { th.Messages[len(th.Messages)-2].Status = "completed" })
	if _, err := c.McpHandle("mcpDelegate", mcpproto.Request{Tool: "delegate", Task: "回去", Thread: 1}); err == nil || !strings.Contains(err.Error(), "delegated this task to you") {
		t.Fatalf("work went back up: %v", err)
	}
	c.DelegationParent[3], c.DelegationParent[1] = 2, 3
	if _, err := c.McpHandle("mcpDelegate", mcpproto.Request{Tool: "delegate", Task: "再往下"}); err == nil || !strings.Contains(err.Error(), "limited to") {
		t.Fatalf("delegation went too deep: %v", err)
	}
}

func TestMCPDelegateTracksOnlyTheCallerWithOrWithoutContext(t *testing.T) {
	for _, noContext := range []bool{false, true} {
		t.Run(fmt.Sprintf("noContext=%v", noContext), func(t *testing.T) {
			c := mcpHost(t)
			other := handoffSource()
			other.ID, other.ServerID = 3, "reference-only"
			c.SetThreads(append(c.Threads(), other))
			task := "给登录补测试 " + model.ReferenceText(testReference(other), other.Title)
			_, err := c.McpDelegate(mcpproto.Request{Tool: "delegate", Thread: 2, Task: task, NoContext: noContext})
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Handoffs) != 1 || c.Handoffs[0].From != 1 || c.Handoffs[0].To != 2 {
				t.Fatalf("delegation must track only its caller: %+v", c.Handoffs)
			}
			if got := c.ThreadWithID(other.ID); len(got.Messages) != len(other.Messages) {
				t.Fatalf("a context reference became a delegating conversation: %+v", got)
			}
			source := c.ThreadWithID(1)
			last := source.Messages[len(source.Messages)-1]
			if last.Kind != "handoff" || len(last.Agents) != 1 || last.Agents[0].Status != subagent.Running || last.Agents[0].Name != "给登录补测试" || conversation.ActivityTitle(last) != "Handed off to conversation \"测试\"" {
				t.Fatalf("explicit delegation not tracked: %+v", last)
			}
			_, snapshots := model.SplitReferenceContext(c.ThreadWithID(2).Queue[0].Text)
			wantSnapshots := 2
			if noContext {
				wantSnapshots = 1
			}
			if len(snapshots) != wantSnapshots {
				t.Fatalf("wrong context snapshots: %+v", snapshots)
			}
			c.Update(2, func(th *model.Thread) {
				th.Busy, th.Queue = false, nil
				th.Messages = append(th.Messages, model.Message{Role: "user", Content: task}, model.Message{Role: "assistant", Content: "测试已补齐\n全部通过"})
			})
			source = c.ThreadWithID(1)
			agent := source.Messages[len(source.Messages)-1].Agents[0]
			if agent.Status != subagent.Completed || agent.Detail != "测试已补齐" {
				t.Fatalf("delegated result not followed: %+v", agent)
			}
		})
	}
}

func TestMCPDelegateNeedsAVisibleCaller(t *testing.T) {
	c := mcpHost(t)
	if _, err := c.McpHandle("mcpDelegate", mcpproto.Request{Tool: "read_thread", Task: "x"}); err == nil || err.Error() != mcpproto.CallerUnknown {
		t.Fatalf("unknown caller not reported: %v", err)
	}
	if _, err := c.McpHandle("mcpDelegate", mcpproto.Request{Tool: "delegate", Task: "x", Agent: "gemini"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown agent accepted: %v", err)
	}
}

func TestMCPListReadAndStatus(t *testing.T) {
	c := mcpHost(t)
	list := c.McpThreads(mcpproto.Request{Tool: "delegate"})
	if len(list.Threads) != 1 || list.Threads[0].ID != 2 || list.Threads[0].Status != subagent.Running {
		t.Fatalf("bad list (the caller must be left out): %+v", list)
	}
	read, err := c.McpRead(mcpproto.Request{Thread: 1})
	if err != nil || !read.Loaded || !strings.Contains(read.Packet, "修复登录过期的问题") {
		t.Fatalf("bad read: %+v %v", read, err)
	}
	if _, err := c.McpRead(mcpproto.Request{Thread: 42}); err == nil {
		t.Fatal("read of a missing conversation succeeded")
	}
	c.Update(1, func(th *model.Thread) { th.Busy = false })
	st, err := c.McpStatus(mcpproto.Request{Thread: 1})
	if err != nil || st.Status != subagent.Completed || !strings.Contains(st.Reply, "时区") {
		t.Fatalf("bad status: %+v %v", st, err)
	}
	// Codex's shared app-server may pass any workspace, such as /.
	if _, err := c.McpHandle("mcpThreads", mcpproto.Request{Workspace: "/"}); err != nil {
		t.Fatalf("a call with the service's workspace was refused: %v", err)
	}
}

func TestMCPPublicThreadIdentifiersAreSessionLinks(t *testing.T) {
	c := mcpHost(t)
	s := mcpserver.NewServer("/ws", func(op string, args, out any) error {
		res, err := c.McpHandle(op, args.(mcpproto.Request))
		if err == nil {
			assign(out, res)
		}
		return err
	}, func(time.Duration) {})
	listing, err := s.CallTool("list_threads", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(listing, testRef(appmodel.AgentCodex, "x").URL()) || strings.Contains(listing, "#2") {
		t.Fatalf("local number exposed: %q %v", listing, err)
	}
	args, _ := json.Marshal(map[string]any{"id": testReference(c.ThreadWithID(1)).URL()})
	if packet, err := s.CallTool("read_thread", args); err != nil || !strings.Contains(packet, "修复登录过期的问题") {
		t.Fatalf("session link unreadable: %s %v", packet, err)
	}
	if _, err := s.CallTool("read_thread", json.RawMessage(`{"id":1}`)); err == nil {
		t.Fatal("accepted a legacy numeric tool argument")
	}
}

func TestAgentsGetTheMiyaServerOnlyInTheService(t *testing.T) {
	if mcpconfig.ClaudeArgs("/ws") != nil || mcpconfig.CodexArgs("/ws") != nil || mcpconfig.ClaudeEnv() != nil {
		t.Fatal("agents got the miya server outside the service")
	}
	mcpconfig.Enabled = true
	t.Cleanup(func() { mcpconfig.Enabled = false })
	args := mcpconfig.ClaudeArgs("/w s")
	if len(args) != 4 || args[0] != "--mcp-config" || args[2] != "--allowedTools" || strings.Contains(args[3], "delegate") {
		t.Fatalf("bad Claude args: %q", args)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(args[1]), &cfg); err != nil || cfg.MCPServers["miya"].Command == "" || strings.Join(cfg.MCPServers["miya"].Args, "|") != "-mcp|/w s|"+svcproto.Socket() {
		t.Fatalf("bad Claude MCP config: %s %v", args[1], err)
	}
	codexArgs := strings.Join(mcpconfig.CodexArgs(`/w "s"`), " ")
	if !strings.Contains(codexArgs, `mcp_servers.miya.args=["-mcp","/w \"s\"",`+mcpconfig.TomlString(svcproto.Socket())+`]`) || !strings.Contains(codexArgs, "tool_timeout_sec=1500") {
		t.Fatalf("bad Codex args: %s", codexArgs)
	}
}
