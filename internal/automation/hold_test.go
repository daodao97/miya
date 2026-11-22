package automation

import (
	"strings"
	"testing"
	"time"
)

// Near a subscription's limit, wakes without data wait; wakes with data,
// and running it now, still start.
func TestTriggeredRunsHoldOffNearTheQuota(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(ingressAutomation(WakePolicy{}))
	var asked []string
	pressure := "Codex has used 95% of its weekly quota"
	w.rn.Hold = func(agent string) string {
		asked = append(asked, agent)
		return pressure
	}
	wk, _ := w.rn.Ingress(a.ID, "", "", false, "", "")
	if wk.Outcome != WakeHeld || !strings.Contains(wk.Detail, "Holding off: "+pressure) {
		t.Fatalf("not held near the quota: %+v", wk)
	}
	if len(asked) != 1 || asked[0] != a.Agent {
		t.Fatalf("asked about %q", asked)
	}
	if wk, err := w.rn.Ingress(a.ID, "", "", false, "issue #15 opened", ""); err != nil || wk.Outcome != WakeStarted {
		t.Fatalf("a wake with data was held: %+v %v", wk, err)
	}
	w.endRun(a.ID)
	w.now = w.now.Add(time.Minute)
	if wk, err := w.rn.Wake(WakeEvent{Automation: a.ID, Source: SourceManual}); err != nil || wk.Outcome != WakeStarted {
		t.Fatalf("run now was held: %+v %v", wk, err)
	}
	w.endRun(a.ID)
	pressure = ""
	w.now = w.now.Add(time.Minute)
	if wk, _ := w.rn.Ingress(a.ID, "", "", false, "", ""); wk.Outcome != WakeStarted {
		t.Fatalf("still held once the quota allowed it: %+v", wk)
	}
}
