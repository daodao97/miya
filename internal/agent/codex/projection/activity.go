package projection

import (
	"encoding/json"

	codex "miya/internal/agent/codex/native"
	"miya/internal/chat/model"
)

// Activity projects the fields understood by shared conversation features and
// retains the rest as opaque compatibility data for older peers and caches.
func Activity(item codex.Item) *model.Activity {
	raw, err := json.Marshal(item)
	var activity model.Activity
	if err == nil && json.Unmarshal(raw, &activity) == nil {
		return &activity
	}
	return &model.Activity{Phase: item.Phase, Server: item.Server, Tool: item.Tool,
		Arguments: item.Arguments, Command: item.Command, Query: item.Query,
		CommandActions: item.CommandActions, Changes: item.Changes}
}

func Plan(steps []codex.PlanStep) []model.PlanStep {
	if steps == nil {
		return nil
	}
	out := make([]model.PlanStep, len(steps))
	for i, step := range steps {
		out[i] = model.PlanStep{Step: step.Step, Status: step.Status}
	}
	return out
}
