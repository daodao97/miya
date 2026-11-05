package main

import (
	"strings"
	"time"
	"unicode/utf8"

	dg "miya/internal/delegation"
	"miya/internal/model"
	"miya/internal/subagent"
)

// Output keeps streaming through the chat path. Persist only changed, bounded
// progress checkpoints, at most once per two seconds (wait changes are urgent).
func (s *collaborationState) checkProgress() {
	if !s.Manager.HasActiveExecutions() {
		return
	}
	value, err := s.OnMain(func() (any, error) {
		var events []dg.Event
		codexEpoch, codexLive := s.host.CodexEpoch()
		for id, run := range s.Executions {
			if !strings.HasPrefix(id, "task-") {
				continue
			}
			t := s.host.Thread(run.Thread)
			if !t.Busy || t.ExecutionID != id {
				continue
			}
			if model.ThreadAgent(t) == agentCodex && (run.Turn == "" || run.Turn != t.TurnID || !codexLive || run.Epoch != codexEpoch) {
				continue
			}
			if model.ThreadAgent(t) == agentClaude && !run.Started {
				continue
			}
			task, err := s.Manager.Read(id)
			if err != nil || task.State != dg.Running {
				continue
			}
			progress, wait := managedProgress(t)
			if s.host.AwaitsUser(t) {
				wait = "approval_or_input"
			}
			if progress == run.progress && wait == run.waitCause {
				continue
			}
			now := time.Now()
			if wait == run.waitCause && now.Sub(run.progressAt) < 2*time.Second {
				continue
			}
			run.progress, run.waitCause, run.progressAt = progress, wait, now
			run.Sequence++
			events = append(events, dg.Event{TaskID: id, Target: task.Target, NativeTurnID: run.Turn, SessionEpoch: run.Epoch, State: dg.Running, Progress: progress, WaitCause: wait, Sequence: run.Sequence, ExpectedRevision: task.Revision})
		}
		return events, nil
	})
	if err != nil {
		return
	}
	_ = s.Manager.ObserveProgress(value.([]dg.Event))
}

func managedProgress(t Thread) (progress, wait string) {
	for _, m := range t.Messages {
		if m.ExecutionID != t.ExecutionID {
			continue
		}
		if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
			text := strings.TrimSpace(m.Content)
			if len(text) > 4096 {
				text = text[len(text)-4096:]
				for len(text) > 0 && !utf8.RuneStart(text[0]) {
					text = text[1:]
				}
			}
			runes := []rune(text)
			if len(runes) > 1024 {
				runes = runes[len(runes)-1024:]
			}
			progress = string(runes)
		}
		for _, agent := range m.Agents {
			if subagent.Active(agent.Status) {
				wait = "sub_agent"
			}
		}
	}
	return progress, wait
}
