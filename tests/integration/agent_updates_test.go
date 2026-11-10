package integration

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/agent"
	"miya/internal/agent/cliupdate"
	"miya/internal/controller"
	svcclient "miya/internal/service/client"
	desktopui "miya/internal/ui"
)

type updateSettingsHost struct {
	desktopui.Host
	info     map[string]cliupdate.Info
	upgrades []string
}

func (h *updateSettingsHost) EnsureAgentUpdates()                     {}
func (h *updateSettingsHost) AgentSettings() []agent.Setting          { return nil }
func (h *updateSettingsHost) AgentUpdates() map[string]cliupdate.Info { return h.info }
func (h *updateSettingsHost) AgentUpdateBusy() bool {
	for _, v := range h.info {
		if v.Updating {
			return true
		}
	}
	return false
}
func (h *updateSettingsHost) Agents() []agent.Availability {
	return []agent.Availability{{ID: "codex", Name: "Codex", Path: "/tools/codex", Enabled: true}, {ID: "pi", Name: "Pi", Path: "/tools/pi"}}
}
func (h *updateSettingsHost) UpgradeAgent(id string) {
	h.upgrades = append(h.upgrades, id)
	v := h.info[id]
	v.Updating = true
	h.info[id] = v
}

func TestAgentSettingsUpgradeButtonAndProgress(t *testing.T) {
	app, _ := testApp(t)
	h := &updateSettingsHost{Host: app.C, info: map[string]cliupdate.Info{
		"codex": {Version: "1.2.3", Latest: "1.3.0", Via: "npm", Command: "npm install -g @openai/codex@latest", Update: true},
		"pi":    {Version: "1.0.0", Latest: "1.1.0", Via: "npm", Update: true},
	}}
	app.C = h
	tt := ui.NewTester(app.Render, 1180, 1400)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("1.2.3") || !tt.HasText("Update command: npm install -g @openai/codex@latest") {
		t.Fatalf("missing version or command: %v", tt.Texts())
	}
	if err := tt.Click("Update to 1.3.0"); err != nil {
		t.Fatal(err)
	}
	if len(h.upgrades) != 1 || h.upgrades[0] != "codex" || !tt.HasText("Updating…") {
		t.Fatalf("upgrade not started: %v / %v", h.upgrades, tt.Texts())
	}
	_ = tt.Click("Update to 1.1.0")
	if len(h.upgrades) != 1 {
		t.Fatal("another update was allowed while busy")
	}
	h.info["codex"] = cliupdate.Info{Version: "1.3.0", Previous: "1.2.3", Latest: "1.3.0", Via: "npm"}
	tt.Frame()
	if !tt.HasText("1.2.3 → 1.3.0") || !tt.HasText("Up to date") {
		t.Fatalf("verified result missing: %v", tt.Texts())
	}
	h.info["codex"] = cliupdate.Info{Version: "1.3.0"}
	tt.Frame()
	if !tt.HasText("Update this CLI using its original installer.") {
		t.Fatal("unknown installer did not show manual update guidance")
	}
}

func TestAgentUpdateStateMirrorsServiceHost(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	info := cliupdate.Info{Path: "/remote/bin/codex", Version: "1.2.3", Latest: "1.3.0", Update: true, Updating: true}
	state := c.SnapshotState()
	state.AgentUpdates = map[string]cliupdate.Info{"codex": info}
	mirror := controller.NewMirrorController()
	defer mirror.Cancel()
	mirror.Remote = svcclient.NewClosedClient()
	mirror.ApplyState(state)
	if path, _ := mirror.AgentInstall("codex"); path != info.Path {
		t.Fatalf("window inspected its local CLI instead of service host: %q", path)
	}
	if mirror.AgentUpdates()["codex"] != info || !mirror.AgentUpdateBusy() {
		t.Fatal("upgrade state not mirrored")
	}
	if got := mirror.SnapshotState().AgentUpdates["codex"]; got != info {
		t.Fatalf("snapshot lost upgrade state: %+v", got)
	}
}
