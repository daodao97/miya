package projection

import (
	"encoding/json"
	"reflect"
	"testing"

	codex "miya/internal/agent/codex/native"
)

func TestActivityPreservesLegacyNativeWireFields(t *testing.T) {
	item := codex.Item{ID: "item-1", Type: "mcpToolCall", Cwd: "/workspace", Phase: "commentary",
		Server: "miya", Tool: "memory", Arguments: json.RawMessage(`{"query":"task"}`),
		Result: json.RawMessage(`{"ok":true}`), ReceiverThreadIDs: []string{"child-1"},
		AggregatedOutput: "preserved legacy output"}
	want, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	activity := Activity(item)
	got, err := json.Marshal(activity)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if json.Unmarshal(want, &a) != nil || json.Unmarshal(got, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("legacy item fields changed:\nold %s\nnew %s", want, got)
	}
	activity.Phase, activity.Tool = "", "updated"
	got, err = json.Marshal(activity)
	if err != nil {
		t.Fatal(err)
	}
	var restored codex.Item
	if err := json.Unmarshal(got, &restored); err != nil || restored.Phase != "" || restored.Tool != "updated" || restored.AggregatedOutput != item.AggregatedOutput {
		t.Fatalf("metadata update lost compatibility fields: %+v, %v", restored, err)
	}
}
