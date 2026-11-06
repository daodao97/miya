package integration

import (
	"errors"
	"testing"

	"miya/internal/codex"
)

func TestWriterConflictMessages(t *testing.T) {
	for _, message := range []string{
		"thread abc already has an active writer",
		"Thread abc already has a live local writer",
	} {
		if !codex.IsWriterConflict(errors.New(message)) {
			t.Fatalf("missed conflict: %s", message)
		}
	}
	for _, err := range []error{nil, errors.New("connection closed"), errors.New("thread not found")} {
		if codex.IsWriterConflict(err) {
			t.Fatalf("misclassified error: %v", err)
		}
	}
}

func TestTurnStartedDoesNotInferExternalOwnership(t *testing.T) {
	c := testController()
	c.Handle(event("turn/started", `{"threadId":"t2","turn":{"id":"turn2"}}`))
	if c.Threads()[1].ReadOnly {
		t.Fatal("turn notification is not a writer conflict")
	}
}
