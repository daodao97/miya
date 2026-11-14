package integration

import (
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"strings"
	"testing"
)

func TestNSGoRetryRejectsUnavailableOrStaleResponse(t *testing.T) {
	for _, state := range []string{"busy", "loading", "pending", "readonly", "stale", "resume-error", "offline", "no-user", "empty-user"} {
		t.Run(state, func(t *testing.T) {
			c := isolatedController(t)
			th := failedResponse()
			switch state {
			case "busy":
				th.Busy = true
			case "loading":
				th.Loading = true
			case "pending":
				th.TaskPending = true
			case "readonly":
				th.ReadOnly = true
			case "stale":
				th.Messages = append(th.Messages, model.Message{ID: 13, Role: "assistant", Content: "已恢复"})
			case "resume-error":
				th.Messages[2].ItemID = "resume-error"
			case "no-user":
				th.Messages = th.Messages[1:]
			case "empty-user":
				th.Agent = appmodel.AgentClaude
				c.ClaudeBinary = "/bin/true"
				th.Messages[0].Content = ""
				// Without earlier output, retry must resend the original message.
				th.Messages = append(th.Messages[:1], th.Messages[2])
			}
			c.SetThreads([]model.Thread{th})
			if err := c.RetryResponse(appmodel.RetryRequest{ThreadID: 1, MessageID: 12}); err == nil {
				t.Fatal("unavailable response was retried")
			}
			if len(c.ThreadWithID(1).Messages) != len(th.Messages) || len(c.ThreadWithID(1).Queue) != 0 {
				t.Fatal("rejected retry changed the conversation")
			}
		})
	}
}

func TestNSGoRetryAfterStreamedOutputContinuesInsteadOfRewinding(t *testing.T) {
	c := isolatedController(t)
	th := failedResponse()
	th.Agent = appmodel.AgentClaude
	c.ClaudeBinary = "/bin/true"
	th.Messages = append(th.Messages[:2], model.Message{ID: 14, Role: "assistant", Content: "已经写好的长回复"}, th.Messages[2])
	c.SetThreads([]model.Thread{th})
	if err := c.RetryResponse(appmodel.RetryRequest{ThreadID: 1, MessageID: 12}); err != nil {
		t.Fatal(err)
	}
	got := c.ThreadWithID(1).Messages
	if len(got) < 4 || got[1].ID != 11 || got[2].ID != 14 || got[3].ID != 12 {
		t.Fatalf("retry discarded output streamed before the error: %+v", got)
	}
	var continued bool
	for _, m := range got[4:] {
		if m.Role == "user" && strings.Contains(m.Content, msgfmt.RetryContinuePrompt) {
			continued = true
		}
	}
	if !continued {
		t.Fatalf("retry did not ask the agent to continue: %+v", got)
	}
}
