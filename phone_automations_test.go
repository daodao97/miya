//go:build !windows

package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"miya/internal/remote"
)

func TestPhoneAutomationsReadListAndHistory(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.startAutomations(filepath.Join(t.TempDir(), "automations.json"), false)
	v := mobileAutomationFixture()
	c.auto.store.Automations = []Automation{v.Automation}
	c.auto.store.Runs[v.ID] = v.Runs
	if !c.phoneSnapshot().Automations {
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
			var list []automationView
			if err := json.Unmarshal(f.Data, &list); err != nil || f.Error != "" || len(list) != 1 {
				t.Fatalf("automation list not returned: %v %s %s", err, f.Error, f.Data)
			}
			if list[0].ID != v.ID || list[0].ScheduleText == "" || len(list[0].Runs) != 1 || list[0].Runs[0].Report.Summary != v.Runs[0].Report.Summary {
				t.Fatal("schedule or structured run report lost in transport")
			}
			if len(c.auto.active) != 0 || len(c.threads()) != 0 {
				t.Fatal("viewing automations started a run")
			}
			return
		case <-conn.ctx.Done():
			t.Fatal("no automation response")
		}
	}
}

func TestPhoneAutomationsUnavailableReplies(t *testing.T) {
	c := serverController(t.TempDir(), "")
	if c.phoneSnapshot().Automations {
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
	c.startAutomations(filepath.Join(t.TempDir(), "automations.json"), false)
	v := mobileAutomationFixture()
	v.Project, v.Enabled = t.TempDir(), false
	c.auto.store.Automations = []Automation{v.Automation}
	c.auto.submit = func(r sendRequest) int {
		if r.Project != v.Project || r.Agent != v.Agent || !r.MemoryControl {
			t.Errorf("wrong automation submission: %+v", r)
		}
		id := c.nextID()
		c.projectCache[r.Project] = []Thread{{ID: id, ServerID: "phone-run", Agent: r.Agent, Busy: true}}
		return id
	}
	if !c.phoneSnapshot().AutomationRun {
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
		var result phoneAutomationRunResult
		if err := json.Unmarshal(f.Data, &result); err != nil || result.RequestID != request {
			t.Fatalf("bad acknowledgement: %v %s", err, f.Data)
		}
		if (f.Error == "") != (request == "start") {
			t.Fatalf("unexpected %s result: %s", request, f.Error)
		}
		if len(result.List) != 1 || !result.List[0].Running || len(result.List[0].Runs) != 1 || result.List[0].Enabled {
			t.Fatalf("run not returned or schedule changed: %+v", result.List)
		}
		if result.List[0].Runs[0].Result != runRunning || result.List[0].Runs[0].Thread == 0 || c.cwd == v.Project {
			t.Fatal("manual run did not start a background conversation")
		}
	}
}

func TestPhoneAutomationRunUnavailable(t *testing.T) {
	c := serverController(t.TempDir(), "")
	if c.phoneSnapshot().AutomationRun {
		t.Fatal("unavailable run capability advertised")
	}
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "automation-run", AutomationID: "missing", RequestID: "unavailable"}
	for {
		select {
		case f := <-conn.frames:
			if f.Type == "automation-run" {
				var result phoneAutomationRunResult
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
