package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/mcp/service"
	"miya/internal/model"
)

type snapshotHost struct {
	service.Host
	threads []model.Thread
}

func (h *snapshotHost) AllThreads() []model.Thread { return h.threads }

func TestCallerMatchingUsesConversationSnapshotsWithoutController(t *testing.T) {
	message := model.Message{ID: 4, Role: "tool", Kind: "claudeTool", Status: "inProgress", Content: "mcp__miya__search_memory\n{\"query\":\"project\"}"}
	host := &snapshotHost{threads: []model.Thread{{ID: 1, Messages: []model.Message{message}}}}
	api := service.New(host)
	raw := json.RawMessage(`{"query":"project"}`)
	thread, call, err := api.CallerWithArguments("search_memory", raw)
	if err != nil || thread.ID != 1 || call.ID != 4 {
		t.Fatalf("caller was not resolved: %v %+v %+v", err, thread, call)
	}
	host.threads = append(host.threads, model.Thread{ID: 2, Messages: []model.Message{message}})
	if _, _, err := api.CallerWithArguments("search_memory", raw); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate identity accepted: %v", err)
	}
	if _, _, err := api.CallerWithArguments("search_memory", json.RawMessage(`null`)); err == nil {
		t.Fatal("non-object arguments accepted")
	}
}
