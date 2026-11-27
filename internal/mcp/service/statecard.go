package service

import (
	"encoding/json"
	"errors"
	"strings"

	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/statecard"
)

// StateCard writes the calling conversation's state card for update_status.
// A conversation writes only its own card, found by the call itself, so
// nobody is asked.
func (api *Service) StateCard(r mcpproto.Request) (any, error) {
	caller, call, err := api.CallerWithArguments(r.Tool, r.Arguments)
	if err != nil {
		return nil, err
	}
	var a mcpproto.StatusArgs
	dec := json.NewDecoder(strings.NewReader(string(r.Arguments)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, err
	}
	// An automation's run says where it stands with report_result.
	if caller.Automation != "" {
		return nil, errors.New("A conversation an automation started reports with report_result. It has no state card.")
	}
	card, err := statecard.Normalize(statecard.Card{Purpose: a.Purpose, Done: a.Done, Remaining: a.Remaining, WaitingFor: a.WaitingFor, Checked: a.Checked, References: a.References})
	if err != nil {
		return nil, err
	}
	if card, err = api.host.WriteStateCard(caller, call, card); err != nil {
		return nil, err
	}
	return "State card saved. Other conversations see it as your own account; Miya has not verified it.\n" + statecard.Line(card), nil
}
