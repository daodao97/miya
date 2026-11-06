package coordinator

import (
	"errors"
	"strings"

	codex "miya/internal/agent/codex/native"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/chat/subagent"
	dg "miya/internal/delegation"
)

func CodexFinalPhase(phase string) bool { return phase == "" || phase == "final_answer" }

func LongRunning(t model.Thread) bool {
	for _, task := range t.Tasks {
		if task.Kind == "goal" && task.Status == "active" {
			return true
		}
		if task.Kind == "loop" && (task.Status == "waiting" || task.Status == "running" || task.Status == "active" || task.Status == "starting") {
			return true
		}
	}
	for _, m := range t.Messages {
		for _, agent := range m.Agents {
			if subagent.Active(agent.Status) || agent.Status == subagent.Unconfirmed {
				return true
			}
		}
	}
	return false
}

func NativeResult(messages []model.Message, phases bool) dg.Result {
	r := dg.Result{Artifacts: msgfmt.ChangedFiles(messages)}
	for _, m := range messages {
		if m.Role == "error" {
			r.Error = m.Content
		}
		if m.Role != "assistant" || strings.TrimSpace(m.Content) == "" {
			continue
		}
		if phases && m.Item != nil && !CodexFinalPhase(m.Item.Phase) {
			continue
		}
		r.Text = m.Content
	}
	return r
}

// Generic native/internal errors can occur after side effects. Only protocol
// validation/method rejection proves a sent start request was not accepted.
func ProvenCodexRejection(err error) bool {
	var rpc *codex.RPCError
	return errors.As(err, &rpc) && (rpc.Code == -32600 || rpc.Code == -32601 || rpc.Code == -32602)
}
