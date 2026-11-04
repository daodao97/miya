package main

import (
	"strings"
	"time"
	"unicode/utf8"

	dg "miya/internal/delegation"
)

// Output keeps streaming through the chat path. Persist only changed, bounded
// progress checkpoints, at most once per two seconds (wait changes are urgent).
func (s *collaborationState) checkProgress() {
	if !s.manager.HasActiveExecutions() {
		return
	}
	value, err := s.onMain(func() (any, error) {
		c := s.c
		var events []dg.Event
		for id, run := range c.managedExecutions {
			if !strings.HasPrefix(id, "task-") {
				continue
			}
			t := c.threadWithID(run.thread)
			if !t.Busy || t.ExecutionID != id {
				continue
			}
			if threadAgent(t) == agentCodex && (run.turn == "" || run.turn != t.TurnID || c.client == nil || run.epoch != c.client.RuntimeEpoch()) {
				continue
			}
			if threadAgent(t) == agentClaude && !run.started {
				continue
			}
			task, err := s.manager.Read(id)
			if err != nil || task.State != dg.Running {
				continue
			}
			progress, wait := managedProgress(t)
			if c.threadAwaitsUser(t) {
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
			run.sequence++
			events = append(events, dg.Event{TaskID: id, Target: task.Target, NativeTurnID: run.turn, SessionEpoch: run.epoch, State: dg.Running, Progress: progress, WaitCause: wait, Sequence: run.sequence, ExpectedRevision: task.Revision})
		}
		return events, nil
	})
	if err != nil {
		return
	}
	_ = s.manager.ObserveProgress(value.([]dg.Event))
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
			if subAgentActive(agent.Status) {
				wait = "sub_agent"
			}
		}
	}
	return progress, wait
}
