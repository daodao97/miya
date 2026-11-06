package manager

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	claude "miya/internal/agent/claude/native"
	"miya/internal/memory"
	"miya/internal/model"
)

type Turn struct {
	LocalID      string
	FirstMessage int
}

func TaskKey(t model.Thread, session string) string {
	for _, task := range t.Tasks {
		if task.Kind == "goal" && task.ID != "" && (task.Status == "running" || task.Status == "in_progress" || task.Status == "active") {
			sum := sha256.Sum256([]byte(task.Title))
			return fmt.Sprintf("session:%s:task:%s:%x", session, task.ID, sum[:8])
		}
	}
	return "session:" + session
}

func (mg *Manager) BeginTurn(t model.Thread, req model.SendRequest, firstMessage int) string {
	if mg.Store == nil || t.MemoryDisabled || t.Automation != "" || req.MemoryControl || req.KeepSessionSettings || Command(req.Text) {
		// A failed turn never completes, so its record would otherwise attach the
		// next skipped turn's output to the failed turn's messages.
		if !req.Steer {
			delete(mg.Turns, t.ID)
		}
		return ""
	}
	if mg.Turns == nil {
		mg.Turns = map[int]Turn{}
	}
	if req.Steer {
		return mg.Turns[t.ID].LocalID
	}
	turn := Turn{claude.NewSessionID(), firstMessage}
	mg.Turns[t.ID] = turn
	return turn.LocalID
}

func (mg *Manager) CompletedTurn(id int, backendTurn string) {
	turn, ok := mg.Turns[id]
	if !ok || mg.Engine == nil || backendTurn == "" {
		return
	}
	t := mg.Host.Thread(id)
	session := mg.Host.ThreadRef(t).URL()
	key := session + ":" + backendTurn
	if mg.results[key] {
		return
	}
	delete(mg.Turns, id)
	if mg.results == nil {
		mg.results = map[string]bool{}
	}
	mg.results[key] = true
	if len(mg.results) > 1000 {
		mg.results = map[string]bool{key: true}
	}
	if t.StopRequested || t.MemoryDisabled || t.Automation != "" {
		return
	}
	scope, err := memory.ProjectScope(mg.Host.Workspace(id))
	if err != nil {
		return
	}
	job := memory.ExtractionJob{Session: session, TurnID: backendTurn, TaskKey: TaskKey(t, session)}
	var tools []memory.Evidence
	var final *memory.Evidence
	for _, m := range t.Messages {
		if m.MemoryControl {
			continue
		}
		if m.ID < turn.FirstMessage {
			continue
		}
		kind := ""
		switch m.Role {
		case "user":
			kind = "user_statement"
		case "assistant":
			kind = "assistant_report"
		case "tool":
			if m.Status == "completed" && !strings.Contains(strings.ToLower(m.Content), "mcp__miya__") && !(m.Item != nil && m.Item.Server == "miya") {
				kind = "tool_result"
			}
		}
		if kind == "" {
			continue
		}
		text := m.Content
		if m.Role == "user" {
			text = memory.StripContext(text)
			text, _ = model.SplitReferenceContext(text)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		src := memory.Source{Thread: session, TurnID: backendTurn, LocalTurnID: turn.LocalID, MessageID: m.ItemID, EvidenceKind: kind, ObservedAt: time.UnixMilli(m.SentAtMs)}
		if src.ObservedAt.IsZero() || m.SentAtMs == 0 {
			src.ObservedAt = time.Now()
		}
		if src.MessageID == "" || src.MessageID == "pending-user" {
			src.MessageID = ""
			src.Locator = fmt.Sprintf("local-message:%d", m.ID)
			src.Limited = true
		}
		ev := memory.Evidence{ID: fmt.Sprintf("message:%d", m.ID), Kind: kind, Text: text, Source: src}
		switch kind {
		case "user_statement":
			job.Evidence = append(job.Evidence, ev)
		case "assistant_report":
			copy := ev
			final = &copy
		case "tool_result":
			if !strings.Contains(text, "[earlier output omitted]") && !strings.Contains(text, "[Content too long") {
				tools = append(tools, ev)
			}
		}
	}
	if len(job.Evidence) == 0 || final == nil {
		return
	}
	job.Evidence = append(job.Evidence, *final)
	// A long final answer is shortened instead of discarding the user's
	// statements; quotes only need to be substrings of the retained text. If the
	// user statements alone exceed the budget, the queue records the skip.
	for !job.FitsEvidenceBudget() {
		last := &job.Evidence[len(job.Evidence)-1]
		b, _ := json.Marshal(job)
		runes := []rune(last.Text)
		keep := len(runes) - (utf8.RuneCount(b) - memory.ExtractionEvidenceBudget) - 16
		if keep <= 0 {
			break
		}
		last.Text = string(runes[:keep])
	}
	for _, ev := range tools {
		candidate := job
		candidate.Evidence = append(append([]memory.Evidence(nil), job.Evidence...), ev)
		if candidate.FitsEvidenceBudget() {
			job = candidate
		}
	}
	mg.Calls++
	engine := mg.Engine
	go func() {
		if _, err := mg.Store.EnqueueExtraction(scope, job); err != nil {
			log.Printf("memory extraction enqueue failed: %v", err)
		}
		mg.Host.Dispatch(func() {
			mg.Calls--
			mg.Host.Changed()
		})
		engine.Wakeup()
	}()
}
