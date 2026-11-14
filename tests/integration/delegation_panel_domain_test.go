package integration

import (
	"encoding/json"
	mobile "miya/internal/phone/client"
	"testing"
	"time"
)

func TestMobileCollaborationResponsesAreCorrelatedAndCancelledOnDisconnect(t *testing.T) {
	m := testPhoneClient()
	var first, second int
	timerA := time.AfterFunc(time.Hour, func() {})
	timerB := time.AfterFunc(time.Hour, func() {})
	m.CollaborationCalls = map[string]*mobile.CollaborationCall{
		"first":  {Timer: timerA, Done: func(json.RawMessage, error) { first++ }},
		"second": {Timer: timerB, Done: func(json.RawMessage, error) { second++ }},
	}
	m.ReceiveCollaboration(json.RawMessage(`{"request_id":"second","result":{"accepted":true}}`), "")
	if first != 0 || second != 1 || timerB.Stop() {
		t.Fatal("response matched wrong request or retained timeout")
	}
	m.InterruptCollaborationRequests()
	if first != 1 || second != 1 || len(m.CollaborationCalls) != 0 || timerA.Stop() {
		t.Fatal("disconnect retained pending callback/timeout")
	}
}
