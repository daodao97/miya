//go:build !windows

package integration

import (
	"testing"
	"time"

	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/memory"
)

func TestMemoryAutomaticSaveOnlyInvalidatesLibrary(t *testing.T) {
	c, d := queueController(t)
	attachMemory(t, c)
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "session", Messages: []model.Message{{ID: 10, Role: "user", Content: "项目背景"}, {ID: 11, Role: "assistant", Content: "已处理"}}}})
	scope, _ := memory.ProjectScope(c.Cwd)
	now := time.Now()
	e, err := c.Memories().Store.Save(scope, memory.Mutation{Kind: "fact", Content: "稳定项目约束", Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}, ObservedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	engine := controller.NewMemoryEngine(c)
	defer engine.Stop()
	before := c.MemoryRevision
	c.MemorySaved(scope, memory.ExtractionJob{Session: c.ThreadRef(c.ThreadWithID(1)).URL()}, []memory.Entry{e})
	d.drain()
	if c.MemoryRevision <= before {
		t.Fatal("automatic save did not refresh the library")
	}
	if len(c.ThreadWithID(1).Messages) != 2 {
		t.Fatal("background extraction inserted a conversation item")
	}
	entries, _, _, err := c.Memories().Store.List(scope)
	if err != nil || len(entries) != 1 {
		t.Fatal("background memory is not available in management", err)
	}
}
