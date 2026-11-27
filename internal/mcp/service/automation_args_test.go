package service

import (
	"encoding/json"
	"testing"

	"miya/internal/automation"
	mcpproto "miya/internal/mcp/protocol"
)

// Every setting the tool schema offers reaches the automation; ones the
// call leaves out keep what the automation had.
func TestAutomationArgsReachTheAutomation(t *testing.T) {
	var args mcpproto.AutomationArgs
	raw := `{"name":"Fix CI","instructions":"fix it","stallAfter":3,"notifyEveryRun":true,"recheck":true,"access":"external"}`
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	var a automation.Automation
	if err := applyWakeArgs(&a, nil, args); err != nil {
		t.Fatal(err)
	}
	if a.StallAfter != 3 || !a.NotifyEveryRun || !a.Recheck || a.Access != automation.AccessExternal {
		t.Fatalf("settings lost: %+v", a)
	}
	kept := automation.Automation{StallAfter: 4, Recheck: true, Access: automation.AccessRead}
	if err := applyWakeArgs(&kept, nil, mcpproto.AutomationArgs{}); err != nil {
		t.Fatal(err)
	}
	if kept.StallAfter != 4 || !kept.Recheck || kept.Access != automation.AccessRead {
		t.Fatalf("an update without them changed them: %+v", kept)
	}
}

// notify picks one of three tiers and overrides notifyEveryRun.
func TestAutomationNotifyTiers(t *testing.T) {
	for raw, want := range map[string][2]bool{
		`{"notify":"always"}`:                          {true, false},
		`{"notify":"attention","notifyEveryRun":true}`: {false, false},
		`{"notify":"never","notifyEveryRun":true}`:     {false, true},
		`{"notifyEveryRun":true}`:                      {true, false},
	} {
		var args mcpproto.AutomationArgs
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			t.Fatal(err)
		}
		a := automation.Automation{NotifyNever: true}
		if err := applyWakeArgs(&a, nil, args); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if a.NotifyEveryRun != want[0] || a.NotifyNever != want[1] {
			t.Errorf("%s: every %v, never %v; want %v", raw, a.NotifyEveryRun, a.NotifyNever, want)
		}
	}
	bad := "sometimes"
	if err := applyWakeArgs(&automation.Automation{}, nil, mcpproto.AutomationArgs{Notify: &bad}); err == nil {
		t.Fatal("an unknown tier was accepted")
	}
}
