package integration

import (
	"miya/internal/agent/cliupdate"
	"miya/internal/controller"
	svcclient "miya/internal/service/client"
	"testing"
)

func TestAgentUpdateStateMirrorsServiceHost(t *testing.T) {
	c := isolatedController(t)
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
