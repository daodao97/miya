package service

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"miya/internal/async"
	mcpproto "miya/internal/mcp/protocol"
)

func (api *Service) followUp(r mcpproto.Request) (any, error) {
	var args mcpproto.FollowUpArgs
	dec := json.NewDecoder(strings.NewReader(string(r.Arguments)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, errors.New("Invalid follow-up arguments: " + err.Error())
	}
	at, err := time.Parse(time.RFC3339, args.At)
	if err != nil {
		return nil, errors.New("Supply a due time in RFC 3339 format, including its timezone")
	}
	caller, call, err := api.CallerWithArguments("follow_up", r.Arguments)
	if err != nil {
		return nil, err
	}
	if caller.Automation != "" || strings.HasPrefix(caller.ExecutionID, "task-") {
		return nil, errors.New("Follow-ups are for ongoing conversations. An automation or delegated task should report its result.")
	}
	ref, scheduler := api.host.ThreadRef(caller).URL(), api.host.Collaboration()
	if ref == "" || call.ItemID == "" || scheduler == nil {
		return nil, errors.New("A verified conversation and tool call are required to schedule a follow-up")
	}
	schedule := scheduler.ScheduleFollowUp(ref, call.TurnID+"\n"+call.ItemID, at, args.Note)
	return async.Reply(func() (any, error) {
		d, err := schedule()
		if err != nil {
			return nil, err
		}
		return mcpproto.FollowUpResult{ID: d.ID, At: d.FollowUpAt, State: string(d.State)}, nil
	}), nil
}
