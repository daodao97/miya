//go:build !mygo_ui

package integration

import (
	"encoding/json"
	"miya/internal/appmodel"
	"miya/internal/delegation/coordinator"
	widgets "miya/internal/nsrequests"
	"strings"
	"testing"
)

func TestNSGoCollaborationInvalidArgumentsReleasePendingRequest(t *testing.T) {
	p := (&widgets.CollaborationUI{}).Pane("source")
	p.Submit("send_thread", map[string]any{"thread": "wrong field", "task": "follow up"}, func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
		_, err := coordinator.Decode(r.Operation, r.Arguments)
		done(nil, err)
	})
	if p.Pending != nil || p.Busy || !strings.Contains(p.Err, "INVALID_ARGUMENT:") {
		t.Fatal("invalid request left the panel locked", p.Err)
	}
}
