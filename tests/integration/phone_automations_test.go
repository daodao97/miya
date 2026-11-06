//go:build !windows

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

func TestPhoneAutomationsReadListAndHistory(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.StartAutomations(filepath.Join(t.TempDir(), "automations.json"), false)
	v := mobileAutomationFixture()
	c.Auto.Store.Automations = []automation.Automation{v.Automation}
	c.Auto.Store.Runs[v.ID] = v.Runs
	if !c.PhoneSnapshot().Automations {
		t.Fatal("automation capability not advertised")
	}
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "automations"}
	for {
		select {
		case f := <-conn.frames:
			if f.Type != "automations" {
				continue
			}
			var list []automation.View
			if err := json.Unmarshal(f.Data, &list); err != nil || f.Error != "" || len(list) != 1 {
				t.Fatalf("automation list not returned: %v %s %s", err, f.Error, f.Data)
			}
			if list[0].ID != v.ID || list[0].ScheduleText == "" || len(list[0].Runs) != 1 || list[0].Runs[0].Report.Summary != v.Runs[0].Report.Summary {
				t.Fatal("schedule or structured run report lost in transport")
			}
			if len(c.Auto.Active) != 0 || len(c.Threads()) != 0 {
				t.Fatal("viewing automations started a run")
			}
			return
		case <-conn.ctx.Done():
			t.Fatal("no automation response")
		}
	}
}

func nextPhoneAutomationForm(t *testing.T, conn *testPhoneConnection, op string) (phoneproto.AutomationFormResult, remote.Frame) {
	t.Helper()
	for {
		select {
		case f := <-conn.frames:
			if f.Type != op {
				continue
			}
			var result phoneproto.AutomationFormResult
			if err := json.Unmarshal(f.Data, &result); err != nil {
				t.Fatal(err)
			}
			return result, f
		case <-conn.ctx.Done():
			t.Fatal("no automation form response")
			return phoneproto.AutomationFormResult{}, remote.Frame{}
		}
	}
}

func TestPhoneAutomationDraftCreateAndRetry(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(dir, "release")
	bin := filepath.Join(dir, "draft-agent")
	script := `#!/bin/sh
while [ ! -f "$MIYA_DRAFT_GATE" ]; do sleep 0.01; done
printf '%s\n' '{"structured_output":{"name":"每日检查","schedule":"0 9 * * 1-5","check":"go test ./...","rounds":3,"instructions":"检查新 Issue。","time_missing":false}}'
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c := serverController(dir, bin)
	c.ClaudeEnv = []string{"MIYA_DRAFT_GATE=" + gate}
	path := filepath.Join(dir, "automations.json")
	c.StartAutomations(path, false)
	if !c.PhoneSnapshot().AutomationCreate {
		t.Fatal("creation capability not advertised")
	}
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "automation-draft", RequestID: "draft", Text: "每个工作日 9 点检查 Issue", Project: dir, Agent: appmodel.AgentClaude}
	conn.requests <- remote.Request{Op: "automations"}
	// A slow agent must not block list refreshes or the rest of the phone.
	listReceived := false
	for !listReceived {
		select {
		case f := <-conn.frames:
			if f.Type == "automation-draft" {
				t.Fatal("agent ran before its gate was released")
			}
			listReceived = f.Type == "automations"
		case <-conn.ctx.Done():
			t.Fatal("draft blocked the phone connection")
		}
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	result, frame := nextPhoneAutomationForm(t, conn, "automation-draft")
	if frame.Error != "" || result.RequestID != "draft" || result.View == nil || result.View.Project != dir || result.View.Agent != appmodel.AgentClaude || result.View.Rounds != 3 {
		t.Fatalf("bad draft response: %+v %s", result, frame.Error)
	}
	if len(automation.Load(path).Automations) != 0 {
		t.Fatal("drafting saved an automation before confirmation")
	}
	a := result.View.Automation
	a.Name, a.Enabled, a.NoCarryOver = "手机检查", true, true
	raw, _ := json.Marshal(a)
	r := remote.Request{Op: "automation-create", RequestID: "0011223344556677", Automation: raw}
	conn.requests <- r
	result, frame = nextPhoneAutomationForm(t, conn, r.Op)
	if frame.Error != "" || result.View == nil || result.View.Name != a.Name || !result.View.Enabled || !result.View.NoCarryOver || result.View.Next.IsZero() {
		t.Fatalf("creation failed: %+v %s", result, frame.Error)
	}
	saved := result.View.Automation
	if saved.ID == a.ID || saved.Created.IsZero() || saved.Due.IsZero() {
		t.Fatal("creation trusted draft identity or lost scheduler metadata")
	}
	// The same key is safe after losing a reply, even if a caller sends
	// another payload. It must never edit the automation it already created.
	a.Name = "重复请求不能覆盖"
	r.Automation, _ = json.Marshal(a)
	conn.requests <- r
	result, frame = nextPhoneAutomationForm(t, conn, r.Op)
	if frame.Error != "" || result.View.ID != saved.ID || result.View.Name != saved.Name || len(automation.Load(path).Automations) != 1 {
		t.Fatal("retry duplicated or overwrote a saved automation")
	}
	// Persistence also carries the retry identity across a service restart.
	restarted := serverController(dir, "")
	restarted.StartAutomations(path, false)
	frame = restarted.PhoneAutomationCreate(r)
	if frame.Error != "" || len(restarted.Auto.Store.Automations) != 1 || restarted.Auto.Store.Automations[0].Created != saved.Created {
		t.Fatal("retry after service restart duplicated the automation")
	}
	a.Schedule = "invalid"
	r.Automation, _ = json.Marshal(a)
	r.RequestID = "aabbccddeeff0011"
	conn.requests <- r
	_, frame = nextPhoneAutomationForm(t, conn, r.Op)
	if frame.Error == "" || len(automation.Load(path).Automations) != 1 {
		t.Fatal("invalid schedule was saved")
	}
}

func TestPhoneAutomationCreateUnavailable(t *testing.T) {
	c := serverController(t.TempDir(), "")
	if c.PhoneSnapshot().AutomationCreate {
		t.Fatal("unavailable creation capability advertised")
	}
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "automation-create", RequestID: "0011223344556677"}
	result, frame := nextPhoneAutomationForm(t, conn, "automation-create")
	if frame.Error == "" || result.RequestID != "0011223344556677" {
		t.Fatal("unavailable creation did not return a correlated error")
	}
	conn.requests <- remote.Request{Op: "automation-draft", RequestID: "draft"}
	result, frame = nextPhoneAutomationForm(t, conn, "automation-draft")
	if frame.Error == "" || result.RequestID != "draft" {
		t.Fatal("unavailable draft did not return a correlated error")
	}
}

func TestPhoneAutomationsUnavailableReplies(t *testing.T) {
	c := serverController(t.TempDir(), "")
	if c.PhoneSnapshot().Automations {
		t.Fatal("unsupported automation capability advertised")
	}
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "automations"}
	for {
		select {
		case f := <-conn.frames:
			if f.Type == "automations" {
				if f.Error == "" {
					t.Fatal("unavailable automation service returned success")
				}
				return
			}
		case <-conn.ctx.Done():
			t.Fatal("no unavailable automation response")
		}
	}
}

func TestPhoneAutomationRunStartsInItsProjectAndRejectsOverlap(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.StartAutomations(filepath.Join(t.TempDir(), "automations.json"), false)
	v := mobileAutomationFixture()
	v.Project, v.Enabled = t.TempDir(), false
	c.Auto.Store.Automations = []automation.Automation{v.Automation}
	c.Auto.Submit = func(r model.SendRequest) int {
		if r.Project != v.Project || r.Agent != v.Agent || !r.MemoryControl {
			t.Errorf("wrong automation submission: %+v", r)
		}
		id := c.NextID()
		c.ProjectCache[r.Project] = []model.Thread{{ID: id, ServerID: "phone-run", Agent: r.Agent, Busy: true}}
		return id
	}
	if !c.PhoneSnapshot().AutomationRun {
		t.Fatal("run capability not advertised")
	}
	conn, _ := serveTestPhone(t, c)
	for _, request := range []string{"start", "overlap", "deleted"} {
		id := v.ID
		if request == "deleted" {
			id = "missing"
		}
		conn.requests <- remote.Request{Op: "automation-run", AutomationID: id, RequestID: request}
		var f remote.Frame
		for f.Type != "automation-run" {
			select {
			case f = <-conn.frames:
			case <-conn.ctx.Done():
				t.Fatal("no run acknowledgement")
			}
		}
		var result phoneproto.AutomationRunResult
		if err := json.Unmarshal(f.Data, &result); err != nil || result.RequestID != request {
			t.Fatalf("bad acknowledgement: %v %s", err, f.Data)
		}
		if (f.Error == "") != (request == "start") {
			t.Fatalf("unexpected %s result: %s", request, f.Error)
		}
		if len(result.List) != 1 || !result.List[0].Running || len(result.List[0].Runs) != 1 || result.List[0].Enabled {
			t.Fatalf("run not returned or schedule changed: %+v", result.List)
		}
		if result.List[0].Runs[0].Result != automation.Running || result.List[0].Runs[0].Thread == 0 || c.Cwd == v.Project {
			t.Fatal("manual run did not start a background conversation")
		}
	}
}

func TestPhoneAutomationRunUnavailable(t *testing.T) {
	c := serverController(t.TempDir(), "")
	if c.PhoneSnapshot().AutomationRun {
		t.Fatal("unavailable run capability advertised")
	}
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "automation-run", AutomationID: "missing", RequestID: "unavailable"}
	for {
		select {
		case f := <-conn.frames:
			if f.Type == "automation-run" {
				var result phoneproto.AutomationRunResult
				if json.Unmarshal(f.Data, &result) != nil || result.RequestID != "unavailable" || f.Error == "" {
					t.Fatalf("missing correlated failure: %+v", f)
				}
				return
			}
		case <-conn.ctx.Done():
			t.Fatal("no unavailable run response")
		}
	}
}
