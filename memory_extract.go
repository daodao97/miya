package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"miya/internal/claude"
	"miya/internal/memory"
	"miya/internal/memory/extractor"
	"miya/internal/textutil"
)

type memoryTurn struct {
	LocalID      string
	FirstMessage int
}

func memoryTaskKey(t Thread, session string) string {
	for _, task := range t.Tasks {
		if task.Kind == "goal" && task.ID != "" && (task.Status == "running" || task.Status == "in_progress" || task.Status == "active") {
			sum := sha256.Sum256([]byte(task.Title))
			return fmt.Sprintf("session:%s:task:%s:%x", session, task.ID, sum[:8])
		}
	}
	return "session:" + session
}

func (c *chatController) beginMemoryTurn(t Thread, req SendRequest, firstMessage int) string {
	if c.memory == nil || t.MemoryDisabled || t.Automation != "" || req.MemoryControl || req.KeepSessionSettings || memoryCommand(req.Text) {
		// A failed turn never completes, so its record would otherwise attach the
		// next skipped turn's output to the failed turn's messages.
		if !req.Steer {
			delete(c.memoryTurns, t.ID)
		}
		return ""
	}
	if c.memoryTurns == nil {
		c.memoryTurns = map[int]memoryTurn{}
	}
	if req.Steer {
		return c.memoryTurns[t.ID].LocalID
	}
	turn := memoryTurn{newClaudeSessionID(), firstMessage}
	c.memoryTurns[t.ID] = turn
	return turn.LocalID
}

func (c *chatController) completedMemoryTurn(id int, backendTurn string) {
	turn, ok := c.memoryTurns[id]
	if !ok || c.memoryEngine == nil || backendTurn == "" {
		return
	}
	t := c.threadWithID(id)
	session := c.threadRef(t).URL()
	key := session + ":" + backendTurn
	if c.memoryResults[key] {
		return
	}
	delete(c.memoryTurns, id)
	if c.memoryResults == nil {
		c.memoryResults = map[string]bool{}
	}
	c.memoryResults[key] = true
	if len(c.memoryResults) > 1000 {
		c.memoryResults = map[string]bool{key: true}
	}
	if t.StopRequested || t.MemoryDisabled || t.Automation != "" {
		return
	}
	scope, err := memory.ProjectScope(c.threadWorkspace(id))
	if err != nil {
		return
	}
	job := memory.ExtractionJob{Session: session, TurnID: backendTurn, TaskKey: memoryTaskKey(t, session)}
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
			text, _ = splitReferenceContext(text)
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
	c.memoryCalls++
	engine := c.memoryEngine
	go func() {
		if _, err := c.memory.EnqueueExtraction(scope, job); err != nil {
			log.Printf("memory extraction enqueue failed: %v", err)
		}
		c.dispatch(func() {
			c.memoryCalls--
			c.memoryChanged()
		})
		engine.wakeup()
	}()
}

type memoryEngine struct {
	store   *memory.Store
	ctx     context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	paused  atomic.Bool
	busy    atomic.Bool
	mu      sync.Mutex
	cfg     claude.CLI
	extract func(context.Context, claude.CLI, memory.ExtractionJob) ([]memory.Candidate, error)
	onSaved func(memory.Scope, memory.ExtractionJob, []memory.Entry)
}

func newMemoryEngine(c *chatController) *memoryEngine {
	ctx, cancel := context.WithCancel(c.parentContext())
	e := &memoryEngine{store: c.memory, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), cfg: claude.CLI{Binary: c.claudeBinary, Args: append([]string(nil), c.claudeArgs...), Env: append([]string(nil), c.claudeEnv...)}, extract: extractor.Extract}
	e.onSaved = func(_ memory.Scope, _ memory.ExtractionJob, _ []memory.Entry) {
		// Automatic extraction updates the library, never the conversation.
		c.dispatch(c.memoryChanged)
	}
	go e.loop()
	return e
}

func (e *memoryEngine) wakeup() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
func (e *memoryEngine) configure(cfg claude.CLI) {
	e.mu.Lock()
	e.cfg = cfg
	e.mu.Unlock()
	e.wakeup()
}
func (e *memoryEngine) pause() { e.paused.Store(true) }

func (e *memoryEngine) loop() {
	recoveryNeeded := true
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		if !e.paused.Load() {
			e.busy.Store(true)
			if recoveryNeeded {
				if err := e.store.RecoverExtractions(); err != nil {
					e.busy.Store(false)
					select {
					case <-e.ctx.Done():
						return
					case <-e.wake:
					case <-timer.C:
					}
					continue
				}
				recoveryNeeded = false
			}
			scope, job, ok, err := e.store.ClaimExtraction()
			if err == nil && ok {
				e.mu.Lock()
				cfg := e.cfg
				e.mu.Unlock()
				started := time.Now()
				ctx, cancel := context.WithTimeout(e.ctx, 90*time.Second)
				candidates, runErr := e.extract(ctx, cfg, job)
				cancel()
				if runErr != nil && e.ctx.Err() == nil {
					// Adapter errors carry CLI startup diagnostics, never extraction input.
					log.Printf("memory extraction failed (agent %s, attempt %d): %s", job.ExtractorAgent, job.Attempts, textutil.ClipRunes(strings.Join(strings.Fields(runErr.Error()), " "), 400))
				}
				entries, commitErr := e.store.CompleteExtraction(scope, job.Key, candidates, time.Since(started), runErr, e.ctx.Err() != nil)
				if commitErr != nil {
					entries, commitErr = e.store.CompleteExtraction(scope, job.Key, candidates, time.Since(started), runErr, e.ctx.Err() != nil)
				}
				if commitErr != nil {
					recoveryNeeded = true
				}
				if len(entries) > 0 && e.onSaved != nil {
					e.onSaved(scope, job, entries)
				}
				e.busy.Store(false)
				if e.ctx.Err() != nil {
					return
				}
				if runErr == nil && commitErr == nil {
					continue
				}
			} else {
				e.busy.Store(false)
			}
		}
		select {
		case <-e.ctx.Done():
			return
		case <-e.wake:
		case <-timer.C:
		}
	}
}
