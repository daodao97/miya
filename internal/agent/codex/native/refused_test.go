package codex

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestRefusedOnlyForServerAnswers(t *testing.T) {
	if !Refused(fmt.Errorf("turn/steer: %w", &RPCError{Code: -32600, Message: "no active turn"})) {
		t.Fatal("an RPC error answer is a refusal")
	}
	for _, err := range []error{context.DeadlineExceeded, fmt.Errorf("turn/steer: %w", context.Canceled), errors.New("Codex connection closed: EOF"), nil} {
		if Refused(err) {
			t.Fatalf("%v proves nothing about delivery", err)
		}
	}
}
