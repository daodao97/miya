package coordinator

import (
	"encoding/json"
	"strings"
	"testing"
)

// start_task takes a Bot's name. send_thread addresses a conversation by its
// link, so a Bot there is an argument it does not have.
func TestDecodeTakesABotForStartTaskOnly(t *testing.T) {
	a, err := Decode("start_task", json.RawMessage(`{"request_id":"r1","task":"hi","bot":"Rin"}`))
	if err != nil || a.Bot != "Rin" {
		t.Fatalf("start_task with bot: %+v, %v", a, err)
	}
	_, err = Decode("send_thread", json.RawMessage(`{"request_id":"r1","task":"hi","thread_id":"miya://x/thread/codex/y","bot":"Rin"}`))
	if err == nil || !strings.Contains(err.Error(), "Unsupported send_thread argument bot") {
		t.Fatalf("send_thread with bot: %v", err)
	}
	for _, c := range Commands() {
		_, has := c.Schema["properties"].(map[string]any)["bot"]
		if has != (c.Name == "start_task") {
			t.Errorf("%s: bot in the schema = %v", c.Name, has)
		}
	}
}
