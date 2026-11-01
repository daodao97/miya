package main

import (
	"bufio"
	"encoding/json"
	"errors"
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
	if strings.Join(names, ",") != "list_threads,read_thread,thread_status,delegate,list_automations,create_automation,update_automation,report_result" {
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
	if isErr || !strings.Contains(text, "已交付到对话 #7（Codex）") || !strings.Contains(text, "已完成") || !strings.Contains(text, "测试都加好了") {
		t.Fatalf("bad delegate result: %q", text)
	}
	if strings.Join(ops, ",") != "mcpDelegate,mcpDelegate,mcpStatus,mcpStatus,mcpStatus" {
		t.Fatalf("bad calls: %v", ops)
	}
}

func TestMCPToolErrorsAreToolResults(t *testing.T) {
	call := func(op string, args, out any) error {
		return errors.New("Miya 后台服务没有运行：请先打开 Miya")
	}
	res := mcpExchange(t, call, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_threads","arguments":{}}}`)
	if text, isErr := mcpText(t, res["1"]); !isErr || !strings.Contains(text, "后台服务没有运行") {
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
	if len(target.Queue) != 1 || !strings.HasPrefix(target.Queue[0].Text, "给登录补测试") || !strings.Contains(target.Queue[0].Text, `<miya-thread-context id="1"`) {
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
	if _, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "delegate", Task: "回去", Thread: 1}); err == nil || !strings.Contains(err.Error(), "上游") {
		t.Fatalf("work went back up: %v", err)
	}
	c.delegationParent[3], c.delegationParent[1] = 2, 3
	if _, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "delegate", Task: "再往下"}); err == nil || !strings.Contains(err.Error(), "最多") {
		t.Fatalf("delegation went too deep: %v", err)
	}
}

func TestMCPDelegateNeedsAVisibleCaller(t *testing.T) {
	c := mcpHost(t)
	if _, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "read_thread", Task: "x"}); err == nil || err.Error() != mcpCallerUnknown {
		t.Fatalf("unknown caller not reported: %v", err)
	}
	if _, err := c.mcpHandle("mcpDelegate", mcpRequest{Tool: "delegate", Task: "x", Agent: "gemini"}); err == nil || !strings.Contains(err.Error(), "没有 agent") {
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
