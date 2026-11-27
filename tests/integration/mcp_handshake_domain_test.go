package integration

import (
	"strings"
	"testing"
)

func TestNSGoMCPServerHandshakeAndTools(t *testing.T) {
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
	if strings.Join(names, ",") != "request_desktop_control,runtime_info,list_threads,read_thread,thread_status,delegate,list_automations,create_automation,wake_automation,update_automation,report_result,list_agents,start_task,read_task,wait_task,send_thread,cancel_task,resolve_task,watch_task,unwatch_task,list_tasks,search_memory,remember,forget_memory,update_status,follow_up" {
		t.Fatalf("bad tools: %v", names)
	}
	if res[`"p"`]["result"] == nil || res["3"]["error"].(map[string]any)["code"] != float64(-32601) || res["null"]["error"] == nil {
		t.Fatalf("bad ping, unknown method or parse error: %v", res)
	}
	if len(res) != 5 {
		t.Fatalf("a notification was answered: %v", res)
	}
}
