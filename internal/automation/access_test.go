package automation

import (
	"strings"
	"testing"
)

// A run is sent with its automation's access level, never the window's
// permission; without one it follows the window as before.
func TestRunsAreSentWithTheirAccess(t *testing.T) {
	cases := []struct {
		access Access
		want   string
	}{{"", ""}, {AccessRead, "read"}, {AccessEdit, "edit"}, {AccessExternal, "edit"}}
	for _, tc := range cases {
		w := newWakeHarness(t)
		auto := ingressAutomation(WakePolicy{})
		auto.Access = tc.access
		a := w.add(auto)
		w.runIngress(a.ID, "ping", "x")
		if len(w.h.sent) == 0 || w.h.sent[0].Policy != tc.want {
			t.Fatalf("access %q: sent %+v", tc.access, w.h.sent)
		}
	}
}

func TestUnknownAccessIsRefused(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(ingressAutomation(WakePolicy{}))
	a.Access = "admin"
	if _, err := w.rn.Save(a); err == nil || !strings.Contains(err.Error(), "Unknown access") {
		t.Fatalf("saved with an unknown access: %v", err)
	}
}

// A change to only one of these settings is still a change to confirm.
func TestDiffShowsEverySetting(t *testing.T) {
	w := newWakeHarness(t)
	old := ingressAutomation(WakePolicy{})
	for name, change := range map[string]func(*Automation){
		"May":      func(a *Automation) { a.Access = AccessExternal },
		"Notifies": func(a *Automation) { a.NotifyEveryRun = true },
		"Stalls":   func(a *Automation) { a.StallAfter = 3 },
		"Recheck":  func(a *Automation) { a.Recheck = true },
	} {
		next := old
		change(&next)
		if d := w.rn.Diff(old, next); !strings.HasPrefix(d, name+": ") {
			t.Fatalf("%s: diff %q", name, d)
		}
	}
}
