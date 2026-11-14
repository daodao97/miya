//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"miya/internal/appmodel"
	"miya/internal/remote"
	"testing"
	"time"
)

func collaborationPhoneCall(t *testing.T, f *serviceFixture, operation, from string, args map[string]any) (map[string]any, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw := collaborationJSON(appmodel.CollaborationRequest{Operation: operation, From: from, Arguments: collaborationJSON(args)})
	frame := f.C.PhoneCollaboration(ctx, remote.Request{Op: "collaboration", RequestID: "phone-correlation", Collaboration: raw})
	var response struct {
		RequestID string         `json:"request_id"`
		Result    map[string]any `json:"result"`
	}
	if err := json.Unmarshal(frame.Data, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "phone-correlation" {
		t.Fatal("phone response lost its request correlation")
	}
	if frame.Error != "" {
		return response.Result, fmt.Errorf("%s", frame.Error)
	}
	return response.Result, nil
}
