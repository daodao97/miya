package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"miya/internal/remote"
)

type phoneCollaborationResult struct {
	RequestID string `json:"request_id"`
	Result    any    `json:"result,omitempty"`
}

func collaborationPhoneFrame(requestID string, result any, err error) remote.Frame {
	frame := remote.Frame{Type: "collaboration"}
	frame.Data, _ = json.Marshal(phoneCollaborationResult{RequestID: requestID, Result: result})
	if err != nil {
		frame.Error = err.Error()
	}
	return frame
}

func (c *chatController) phoneCollaboration(ctx context.Context, r remote.Request) remote.Frame {
	var request collaborationRequest
	decoder := json.NewDecoder(bytes.NewReader(r.Collaboration))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return collaborationPhoneFrame(r.RequestID, nil, err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return collaborationPhoneFrame(r.RequestID, nil, errors.New("Invalid collaboration request: unexpected trailing data"))
	}
	type pending struct {
		value any
		err   error
	}
	prepared, ok := onMain(ctx, c.dispatch, func() pending {
		if err := ctx.Err(); err != nil {
			return pending{err: err}
		}
		value, err := c.collaborationUserOp(request)
		return pending{value, err}
	})
	if !ok {
		return collaborationPhoneFrame(r.RequestID, nil, ctx.Err())
	}
	if work, ok := prepared.value.(deferred); ok && prepared.err == nil {
		// Disconnecting the observer releases this connection's worker while the
		// already accepted task can finish. Its request ID remains safe to retry.
		done := make(chan pending, 1)
		go func() { value, err := work(); done <- pending{value, err} }()
		select {
		case prepared = <-done:
		case <-ctx.Done():
			return collaborationPhoneFrame(r.RequestID, nil, ctx.Err())
		}
	}
	return collaborationPhoneFrame(r.RequestID, prepared.value, prepared.err)
}
