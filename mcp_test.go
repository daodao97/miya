package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"miya/internal/codex"
)

// mcpExchange runs the MCP server over the given request lines and returns
// its responses by id.
func mcpExchange(t *testing.T, call func(op string, args, out any) error, lines ...string) map[string]map[string]any {
	t.Helper()
	var out strings.Builder
	if err := serveMCP(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, "/ws", call, func(time.Duration) {}); err != nil {
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
	unknown, statuses := 1, []string{subAgentRunning, subAgentRunning, subAgentCompleted}
	call := func(op string, args, out any) error {
		ops = append(ops, op)
		r := args.(mcpRequest)
		switch op {
		case "mcpDelegate":
			if unknown > 0 {
				unknown--
				return errors.New(mcpCallerUnknown)
			}
			if r.Task != "写测试" || r.Agent != agentCodex || !r.NoContext || r.Workspace != "/ws" || r.Tool != "delegate" {
				t.Errorf("bad delegation: %+v", r)
			}
			assign(out, mcpDelegateResult{Thread: 7, Agent: agentCodex})
		case "mcpStatus":
			status := statuses[0]
			statuses = statuses[1:]
			assign(out, mcpStatusResult{ID: r.Thread, Title: "测试", Agent: agentCodex, Status: status, Reply: "测试都加好了"})
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

func mcpHost(t *testing.T) *chatController {
	c := testController()
	c.active, c.setActive = createSignal(0)
	c.agent, c.setAgent = createSignal(agentCodex)
	src := handoffSource()
	src.ID = 1
	src.Busy = true
	src.Messages = append(src.Messages[:4:4], Message{ID: 9, Role: "tool", Kind: "claudeTool", Status: "inProgress", Content: "mcp__miya__delegate\n{\"task\":\"写测试\"}"})
	c.setThreads([]Thread{src, {ID: 2, Agent: agentCodex, ServerID: "x", Title: "测试", Busy: true, HistoryLoaded: true}})
	return c
}

func TestMCPDelegateHandsWorkWithTheCallersContext(t *testing.T) {
	c := mcpHost(t)
	if caller, ok := c.mcpCaller("delegate"); !ok || caller.ID != 1 {
		t.Fatalf("caller not found: %+v", caller)
	}
	res, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "delegate", Task: "给登录补测试", Thread: 2})
	if err != nil || res.(mcpDelegateResult).Thread != 2 {
		t.Fatalf("delegation failed: %v %v", res, err)
	}
	target := c.threadWithID(2)
	if len(target.Queue) != 1 || !strings.HasPrefix(target.Queue[0].Text, "给登录补测试") || !strings.Contains(target.Queue[0].Text, contextEnvelopeOpen) {
		t.Fatalf("task not sent with the caller's context: %+v", target.Queue)
	}
	source := c.threadWithID(1)
	if last := source.Messages[len(source.Messages)-1]; last.Kind != "handoff" || last.Agents[0].Name != "给登录补测试" {
		t.Fatalf("caller does not follow the work: %+v", last)
	}
	// The receiving conversation cannot hand work back up.
	c.update(2, func(th *Thread) {
		th.Messages = append(th.Messages, Message{ID: c.nextID(), Role: "tool", Kind: "mcpToolCall", Status: "inProgress", Item: &codex.Item{Server: "miya", Tool: "delegate"}})
	})
	c.update(1, func(th *Thread) { th.Messages[len(th.Messages)-2].Status = "completed" })
	if _, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "delegate", Task: "回去", Thread: 1}); err == nil || !strings.Contains(err.Error(), "delegated this task to you") {
		t.Fatalf("work went back up: %v", err)
	}
	c.delegationParent[3], c.delegationParent[1] = 2, 3
	if _, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "delegate", Task: "再往下"}); err == nil || !strings.Contains(err.Error(), "limited to") {
		t.Fatalf("delegation went too deep: %v", err)
	}
}

func TestMCPDelegateTracksOnlyTheCallerWithOrWithoutContext(t *testing.T) {
	for _, noContext := range []bool{false, true} {
		t.Run(fmt.Sprintf("noContext=%v", noContext), func(t *testing.T) {
			c := mcpHost(t)
			other := handoffSource()
			other.ID, other.ServerID = 3, "reference-only"
			c.setThreads(append(c.threads(), other))
			task := "给登录补测试 " + referenceText(testReference(other), other.Title)
			_, err := c.mcpDelegate(mcpRequest{Tool: "delegate", Thread: 2, Task: task, NoContext: noContext})
			if err != nil {
				t.Fatal(err)
			}
			if len(c.handoffs) != 1 || c.handoffs[0].From != 1 || c.handoffs[0].To != 2 {
				t.Fatalf("delegation must track only its caller: %+v", c.handoffs)
			}
			if got := c.threadWithID(other.ID); len(got.Messages) != len(other.Messages) {
				t.Fatalf("a context reference became a delegating conversation: %+v", got)
			}
			source := c.threadWithID(1)
			last := source.Messages[len(source.Messages)-1]
			if last.Kind != "handoff" || len(last.Agents) != 1 || last.Agents[0].Status != subAgentRunning || last.Agents[0].Name != "给登录补测试" || activityTitle(last) != "Handed off to conversation \"测试\"" {
				t.Fatalf("explicit delegation not tracked: %+v", last)
			}
			_, snapshots := splitReferenceContext(c.threadWithID(2).Queue[0].Text)
			wantSnapshots := 2
			if noContext {
				wantSnapshots = 1
			}
			if len(snapshots) != wantSnapshots {
				t.Fatalf("wrong context snapshots: %+v", snapshots)
			}
			c.update(2, func(th *Thread) {
				th.Busy, th.Queue = false, nil
				th.Messages = append(th.Messages, Message{Role: "user", Content: task}, Message{Role: "assistant", Content: "测试已补齐\n全部通过"})
			})
			source = c.threadWithID(1)
			agent := source.Messages[len(source.Messages)-1].Agents[0]
			if agent.Status != subAgentCompleted || agent.Detail != "测试已补齐" {
				t.Fatalf("delegated result not followed: %+v", agent)
			}
		})
	}
}

func TestMCPDelegateNeedsAVisibleCaller(t *testing.T) {
	c := mcpHost(t)
	if _, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "read_thread", Task: "x"}); err == nil || err.Error() != mcpCallerUnknown {
		t.Fatalf("unknown caller not reported: %v", err)
	}
	if _, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "delegate", Task: "x", Agent: "gemini"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown agent accepted: %v", err)
	}
}

func TestMCPListReadAndStatus(t *testing.T) {
	c := mcpHost(t)
	list := c.mcpThreads(mcpRequest{Tool: "delegate"})
	if len(list.Threads) != 1 || list.Threads[0].ID != 2 || list.Threads[0].Status != subAgentRunning {
		t.Fatalf("bad list (the caller must be left out): %+v", list)
	}
	read, err := c.mcpRead(mcpRequest{Thread: 1})
	if err != nil || !read.Loaded || !strings.Contains(read.Packet, "修复登录过期的问题") {
		t.Fatalf("bad read: %+v %v", read, err)
	}
	if _, err := c.mcpRead(mcpRequest{Thread: 42}); err == nil {
		t.Fatal("read of a missing conversation succeeded")
	}
	c.update(1, func(th *Thread) { th.Busy = false })
	st, err := c.mcpStatus(mcpRequest{Thread: 1})
	if err != nil || st.Status != subAgentCompleted || !strings.Contains(st.Reply, "时区") {
		t.Fatalf("bad status: %+v %v", st, err)
	}
	// Codex's shared app-server may pass any workspace, such as /.
	if _, err := c.mcpHandle("mcpThreads", mcpRequest{Workspace: "/"}); err != nil {
		t.Fatalf("a call with the service's workspace was refused: %v", err)
	}
}

func TestMCPPublicThreadIdentifiersAreSessionLinks(t *testing.T) {
	c := mcpHost(t)
	s := &mcpServer{workspace: "/ws", sleep: func(time.Duration) {}, call: func(op string, args, out any) error {
		res, err := c.mcpHandle(op, args.(mcpRequest))
		if err == nil {
			assign(out, res)
		}
		return err
	}}
	listing, err := s.callTool("list_threads", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(listing, testRef(agentCodex, "x").URL()) || strings.Contains(listing, "#2") {
		t.Fatalf("local number exposed: %q %v", listing, err)
	}
	args, _ := json.Marshal(map[string]any{"id": testReference(c.threadWithID(1)).URL()})
	if packet, err := s.callTool("read_thread", args); err != nil || !strings.Contains(packet, "修复登录过期的问题") {
		t.Fatalf("session link unreadable: %s %v", packet, err)
	}
	if _, err := s.callTool("read_thread", json.RawMessage(`{"id":1}`)); err == nil {
		t.Fatal("accepted a legacy numeric tool argument")
	}
}

func TestAgentsGetTheMiyaServerOnlyInTheService(t *testing.T) {
	if claudeMCPArgs("/ws") != nil || codexMCPArgs("/ws") != nil || claudeMCPEnv() != nil {
		t.Fatal("agents got the miya server outside the service")
	}
	miyaMCPEnabled = true
	t.Cleanup(func() { miyaMCPEnabled = false })
	args := claudeMCPArgs("/w s")
	if len(args) != 4 || args[0] != "--mcp-config" || args[2] != "--allowedTools" || strings.Contains(args[3], "delegate") {
		t.Fatalf("bad Claude args: %q", args)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(args[1]), &cfg); err != nil || cfg.MCPServers["miya"].Command == "" || strings.Join(cfg.MCPServers["miya"].Args, "|") != "-mcp|/w s|"+serviceSocket() {
		t.Fatalf("bad Claude MCP config: %s %v", args[1], err)
	}
	codexArgs := strings.Join(codexMCPArgs(`/w "s"`), " ")
	if !strings.Contains(codexArgs, `mcp_servers.miya.args=["-mcp","/w \"s\"",`+tomlString(serviceSocket())+`]`) || !strings.Contains(codexArgs, "tool_timeout_sec=1500") {
		t.Fatalf("bad Codex args: %s", codexArgs)
	}
}
