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
