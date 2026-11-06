package service

import (
	"errors"

	"miya/internal/async"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/model"
	"miya/internal/runtime/tracker"
)

func (api *Service) Runtime(r mcpproto.Request) (any, error) {
	c := api.host
	var caller model.Thread
	count := 0
	for _, t := range c.AllThreads() {
		for _, message := range t.Messages {
			if (message.Status == "inProgress" || message.Status == "in_progress") && isToolCall(message, "runtime_info") {
				caller = t
				count++
				break
			}
		}
	}
	if count == 0 {
		return nil, errors.New(mcpproto.CallerUnknown)
	}
	if count != 1 {
		return nil, errors.New("Multiple conversations are querying runtime information. Unable to identify the caller uniquely. Try again shortly.")
	}
	work, err := c.PrepareRuntime(c.ThreadRef(caller), c.ThreadWorkspace(caller.ID))
	if err != nil {
		return nil, err
	}
	return async.Reply(func() (any, error) {
		data, err := work()
		if err != nil {
			return nil, err
		}
		return data.(tracker.ConversationRuntime).View, nil
	}), nil
}

// Strict identity for every memory operation, including reads: the shared
// Codex app-server's cwd is not the caller's workspace. No time-based guess.
