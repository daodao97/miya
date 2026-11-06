package service

import (
	"encoding/json"
	"errors"
	"reflect"

	"miya/internal/chat/model"
	"miya/internal/chat/presentation"
	mcpproto "miya/internal/mcp/protocol"
)

func (api *Service) Caller(tool string) (model.Thread, bool) {
	c := api.host
	var caller model.Thread
	var at int64
	found := false
	// The caller may be in a background project.
	for _, t := range c.AllThreads() {
		for i := len(t.Messages) - 1; i >= 0; i-- {
			m := t.Messages[i]
			if (m.Status == "inProgress" || m.Status == "in_progress") && isToolCall(m, tool) {
				if !found || m.StartedAtMs > at {
					caller, at, found = t, m.StartedAtMs, true
				}
				break
			}
		}
	}
	return caller, found
}

func (api *Service) CallerWithArguments(tool string, raw json.RawMessage) (model.Thread, model.Message, error) {
	c := api.host
	var want map[string]any
	if json.Unmarshal(raw, &want) != nil || want == nil {
		return model.Thread{}, model.Message{}, errors.New("Tool arguments must be an object")
	}
	type candidate struct {
		thread  model.Thread
		message model.Message
	}
	var matches []candidate
	for _, t := range c.AllThreads() {
		for _, m := range t.Messages {
			if (m.Status != "inProgress" && m.Status != "in_progress") || !isToolCall(m, tool) {
				continue
			}
			var got map[string]any
			invocation, _ := presentation.ToolCall(m)
			_ = json.Unmarshal(invocation.Arguments, &got)
			if got != nil && reflect.DeepEqual(want, got) {
				matches = append(matches, candidate{t, m})
			}
		}
	}
	if len(matches) == 0 {
		return model.Thread{}, model.Message{}, errors.New(mcpproto.CallerUnknown)
	}
	if len(matches) != 1 {
		return model.Thread{}, model.Message{}, errors.New("The calling conversation is ambiguous. Stagger calls with identical arguments and retry.")
	}
	return matches[0].thread, matches[0].message, nil
}

func isToolCall(message model.Message, name string) bool {
	call, ok := presentation.ToolCall(message)
	return ok && call.Server == "miya" && call.Name == name
}
