//go:build !windows

package integration

import (
	"testing"

	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/controller"
	"miya/internal/memory"
	"miya/internal/memory/extractor/extractortest"
	"miya/internal/memory/manager"
	projectmodel "miya/internal/project/model"
)

func TestMemoryCodexExtractorDoesNotRequireClaude(t *testing.T) {
	extractortest.FakeCodex(t)
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	attachMemory(t, c)
	c.ClaudeBinary = ""
	settings := memory.Settings{AutoExtract: true, ExtractorAgent: appmodel.AgentCodex}
	completeMemory(t, c, "memoryConfigure", manager.Request{Settings: &settings})
	list := completeMemory(t, c, "memoryList", manager.Request{}).(manager.List)
	if !list.ExtractorAvailable || list.Settings.ExtractionAgent() != appmodel.AgentCodex || len(list.Extractors) != 2 {
		t.Fatalf("Codex selection still requires Claude: %+v", list)
	}
	for _, option := range list.Extractors {
		if option.Agent == appmodel.AgentClaude && option.Available {
			t.Fatal("unconfigured Claude marked available")
		}
	}
	other := t.TempDir()
	c.SetProjects(append(c.Projects(), projectmodel.Project{Path: other}))
	list = completeMemory(t, c, "memoryList", manager.Request{Project: other}).(manager.List)
	if list.Settings.ExtractionAgent() != appmodel.AgentClaude || list.Settings.AutoExtract {
		t.Fatal("extractor selection leaked to another project")
	}
}

func TestMemoryUnavailableExtractorSwitchDoesNotReplaceWorkingAgent(t *testing.T) {
	t.Setenv("CODEX_BINARY", "/missing/miya-codex")
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	attachMemory(t, c)
	c.ClaudeBinary = "configured-claude"
	settings := memory.Settings{AutoExtract: true, ExtractorAgent: appmodel.AgentClaude}
	completeMemory(t, c, "memoryConfigure", manager.Request{Settings: &settings})
	settings.ExtractorAgent = appmodel.AgentCodex
	value, err := c.Memories().Op("memoryConfigure", manager.Request{Settings: &settings})
	if d, ok := value.(async.Reply); ok && err == nil {
		_, err = d()
	}
	if err == nil {
		t.Fatal("active extraction switched to missing Codex")
	}
	list := completeMemory(t, c, "memoryList", manager.Request{}).(manager.List)
	if list.Settings.ExtractionAgent() != appmodel.AgentClaude || !list.Settings.AutoExtract {
		t.Fatal("failed switch replaced working settings")
	}
	settings.AutoExtract = false
	completeMemory(t, c, "memoryConfigure", manager.Request{Settings: &settings})
}
