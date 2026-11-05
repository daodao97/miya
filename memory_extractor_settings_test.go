//go:build !windows

package main

import (
	"miya/internal/memory"
	"miya/internal/memory/extractor/extractortest"
	"testing"
)

func TestMemoryCodexExtractorDoesNotRequireClaude(t *testing.T) {
	extractortest.FakeCodex(t)
	app, _ := testApp(t)
	c := app.c
	attachMemory(t, c)
	c.claudeBinary = ""
	settings := memory.Settings{AutoExtract: true, ExtractorAgent: agentCodex}
	completeMemory(t, c, "memoryConfigure", memoryRequest{Settings: &settings})
	list := completeMemory(t, c, "memoryList", memoryRequest{}).(memoryList)
	if !list.ExtractorAvailable || list.Settings.ExtractionAgent() != agentCodex || len(list.Extractors) != 2 {
		t.Fatalf("Codex selection still requires Claude: %+v", list)
	}
	for _, option := range list.Extractors {
		if option.Agent == agentClaude && option.Available {
			t.Fatal("unconfigured Claude marked available")
		}
	}
	other := t.TempDir()
	c.setProjects(append(c.projects(), Project{Path: other}))
	list = completeMemory(t, c, "memoryList", memoryRequest{Project: other}).(memoryList)
	if list.Settings.ExtractionAgent() != agentClaude || list.Settings.AutoExtract {
		t.Fatal("extractor selection leaked to another project")
	}
}

func TestMemoryUnavailableExtractorSwitchDoesNotReplaceWorkingAgent(t *testing.T) {
	t.Setenv("CODEX_BINARY", "/missing/miya-codex")
	app, _ := testApp(t)
	c := app.c
	attachMemory(t, c)
	c.claudeBinary = "configured-claude"
	settings := memory.Settings{AutoExtract: true, ExtractorAgent: agentClaude}
	completeMemory(t, c, "memoryConfigure", memoryRequest{Settings: &settings})
	settings.ExtractorAgent = agentCodex
	value, err := c.memoryOp("memoryConfigure", memoryRequest{Settings: &settings})
	if d, ok := value.(deferred); ok && err == nil {
		_, err = d()
	}
	if err == nil {
		t.Fatal("active extraction switched to missing Codex")
	}
	list := completeMemory(t, c, "memoryList", memoryRequest{}).(memoryList)
	if list.Settings.ExtractionAgent() != agentClaude || !list.Settings.AutoExtract {
		t.Fatal("failed switch replaced working settings")
	}
	settings.AutoExtract = false
	completeMemory(t, c, "memoryConfigure", memoryRequest{Settings: &settings})
}
