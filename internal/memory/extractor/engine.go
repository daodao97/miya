package extractor

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"miya/internal/agent/claude/native"
	"miya/internal/memory"
	"miya/internal/textutil"
)

// Engine extracts the queued memory jobs of a store one at a time, in the
// background, until it is stopped.
type Engine struct {
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

func (e *Engine) Wakeup() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) Configure(cfg claude.CLI) {
	e.mu.Lock()
	e.cfg = cfg
	e.mu.Unlock()
	e.Wakeup()
}

func (e *Engine) Pause() { e.paused.Store(true) }

func (e *Engine) loop() {
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

// NewEngine starts an engine for store that asks Claude Code through cfg,
// or Codex, and calls onSaved with the memories each job saved.
func NewEngine(parent context.Context, store *memory.Store, cfg claude.CLI, onSaved func(memory.Scope, memory.ExtractionJob, []memory.Entry)) *Engine {
	ctx, cancel := context.WithCancel(parent)
	e := &Engine{store: store, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), cfg: cfg, extract: Extract}
	e.onSaved = onSaved
	go e.loop()
	return e
}

// NewIdleEngine is an engine for tests that takes wakeups and runs no jobs.
func NewIdleEngine() *Engine { return &Engine{wake: make(chan struct{}, 1)} }

// Stop ends the engine; a job in progress sees its context cancelled.
func (e *Engine) Stop() { e.cancel() }

// Busy reports whether a job is being extracted.
func (e *Engine) Busy() bool { return e.busy.Load() }
