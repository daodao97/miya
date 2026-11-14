//go:build !mygo_ui

package integration

import (
	"miya/internal/appmodel"
	msgfmt "miya/internal/chat/presentation"
	"strings"
	"testing"
)

func TestRetryRecoveryAndMissingFailureDetail(t *testing.T) {
	for _, progress := range []struct{ method, params string }{
		{"item/reasoning/summaryTextDelta", `{"threadId":"t1","turnId":"turn1","itemId":"r","delta":"Summary"}`},
		{"item/completed", `{"threadId":"t1","turnId":"turn1","item":{"id":"a","type":"agentMessage","text":"Recovered"}}`},
	} {
		c := testController()
		c.Handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
		c.Handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":true,"error":{"message":"temporary"}}`))
		if got := msgfmt.ThreadWorkingLabel(c.Threads()[0]); got != appmodel.Tr("Reconnecting…") {
			t.Fatalf("generic retry label = %q", got)
		}
		c.Handle(event(progress.method, progress.params))
		if strings.HasPrefix(c.Threads()[0].Status, "Retrying:") {
			t.Fatal("model activity did not clear retry status")
		}
		c.Handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"failed"}}`))
		th := c.Threads()[0]
		if th.Busy || th.Messages[len(th.Messages)-1].Role != "error" || th.Messages[len(th.Messages)-1].Content == "" {
			t.Fatal("failure without details needs a visible fallback error")
		}
	}
}
