//go:build !mygo_ui

package integration

import (
	"miya/internal/automation"
	"os/exec"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func hasProject(h *automationHarness, path string) bool {
	for _, p := range h.c.Projects() {
		if p.Path == path {
			return true
		}
	}
	return false
}

func TestAutomationWorktreeNeedsRepository(t *testing.T) {
	h := newAutomationHarness(t)
	a := h.add(t, automation.Automation{Name: "Plain", Instructions: "Do", Schedule: "0 9 * * *", Worktree: true})
	_ = h.s.StartRun(a.ID, false)
	run, _ := h.s.Store.LastRun(a.ID)
	if run.Result != automation.Failed || !strings.Contains(run.Summary, "worktree") || len(h.sent) != 0 || len(h.s.Active) != 0 {
		t.Fatalf("run = %+v, sent %d", run, len(h.sent))
	}
}
