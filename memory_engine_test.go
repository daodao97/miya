//go:build !windows

package main

import (
	"testing"
	"time"

	"miya/internal/memory"
)

func TestMemoryAutomaticSaveOnlyInvalidatesLibrary(t *testing.T) {
	c, d := queueController(t)
	attachMemory(t, c)
	c.setThreads([]Thread{{ID: 1, ServerID: "session", Messages: []Message{{ID: 10, Role: "user", Content: "项目背景"}, {ID: 11, Role: "assistant", Content: "已处理"}}}})
	scope, _ := memory.ProjectScope(c.cwd)
	now := time.Now()
	e, err := c.memories().Store.Save(scope, memory.Mutation{Kind: "fact", Content: "稳定项目约束", Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}, ObservedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	engine := newMemoryEngine(c)
	defer engine.Stop()
	before := c.memoryRevision
	c.memorySaved(scope, memory.ExtractionJob{Session: c.threadRef(c.threadWithID(1)).URL()}, []memory.Entry{e})
	d.drain()
	if c.memoryRevision <= before {
		t.Fatal("automatic save did not refresh the library")
	}
	if len(c.threadWithID(1).Messages) != 2 {
		t.Fatal("background extraction inserted a conversation item")
	}
	entries, _, _, err := c.memories().Store.List(scope)
	if err != nil || len(entries) != 1 {
		t.Fatal("background memory is not available in management", err)
	}
}
