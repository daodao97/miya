package mcp_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"miya/internal/mcp"
)

// The public MCP catalog must be complete without importing the Controller.
// Its names and ordering are part of the established tools/list contract.
func TestStandaloneCatalogPreservesToolContract(t *testing.T) {
	want := []string{"request_desktop_control", "runtime_info", "list_threads", "read_thread", "thread_status", "delegate", "list_automations", "create_automation", "update_automation", "report_result", "list_agents", "start_task", "read_task", "wait_task", "send_thread", "cancel_task", "resolve_task", "watch_task", "unwatch_task", "list_tasks", "search_memory", "remember", "forget_memory"}
	var output bytes.Buffer
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n")
	err := mcp.Serve(input, &output, "/workspace", func(string, any, any) error { return errors.New("metadata must not contact the background service") }, func(time.Duration) {})
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	found := false
	for decoder.More() {
		var response struct {
			ID     int `json:"id"`
			Error  any `json:"error"`
			Result struct {
				Tools []struct {
					Name        string         `json:"name"`
					Annotations map[string]any `json:"annotations"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Error != nil {
			t.Fatalf("metadata failed: %+v", response.Error)
		}
		if response.ID != 2 {
			continue
		}
		found = true
		var names []string
		for _, tool := range response.Result.Tools {
			names = append(names, tool.Name)
			if value, ok := tool.Annotations["readOnlyHint"]; !ok || value == nil {
				t.Fatalf("missing access annotation for %s", tool.Name)
			}
		}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("tool catalog changed:\n got %v\nwant %v", names, want)
		}
	}
	if !found {
		t.Fatal("tools/list response missing")
	}
}
