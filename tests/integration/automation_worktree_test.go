package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/automation"
)

// gitProject makes the harness's project a repository with one commit,
// isolated from the person's Git configuration.
func gitProject(t *testing.T, h *automationHarness) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Miya Test")
	t.Setenv("GIT_COMMITTER_NAME", "Miya Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	dir, err := filepath.EvalSymlinks(h.c.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	h.c.Cwd = dir
	gitRun(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	h.s.WorktreeRoot = filepath.Join(t.TempDir(), "worktrees")
	return dir
}

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

func TestAutomationWorktreeRunKeepsChangesUntilMerged(t *testing.T) {
	h := newAutomationHarness(t)
	repo := gitProject(t, h)
	var checkedIn string
	h.s.Check = func(dir, command string) (bool, string) { checkedIn = dir; return true, "ok" }
	a := h.add(t, automation.Automation{Name: "Nightly Fix", Instructions: "Fix it", Schedule: "* * * * *", Check: "make test", Rounds: 2, Worktree: true})
	if err := h.s.StartRun(a.ID, false); err != nil {
		t.Fatal(err)
	}
	run, _ := h.s.Store.LastRun(a.ID)
	if run.Worktree == nil || run.WorktreeState != automation.WorktreeActive || run.Thread == 0 {
		t.Fatalf("run = %+v", run)
	}
	w := *run.Worktree
	if w.Repo != repo || !strings.HasPrefix(w.Branch, "miya/auto/nightly-fix-") || !hasProject(h, w.Dir) {
		t.Fatalf("worktree = %+v, listed %v", w, hasProject(h, w.Dir))
	}
	if got := h.sent[0]; got.Project != w.Dir || !strings.Contains(got.Text, w.Branch) {
		t.Fatalf("sent = %+v", got)
	}
	// A schedule passing meanwhile is skipped without touching this run.
	h.now = h.now.Add(2 * time.Minute)
	h.s.Tick()
	if runs := h.s.Store.Runs[a.ID]; len(runs) != 2 || runs[1].Result != automation.Skipped {
		t.Fatalf("runs = %+v", runs)
	}
	// The agent changes a file in its worktree; the project is untouched.
	if err := os.WriteFile(filepath.Join(w.Dir, "a.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.endTurn(run.Thread, "Fixed")
	h.s.Tick()
	if checkedIn != w.Dir {
		t.Fatalf("check ran in %q, want %q", checkedIn, w.Dir)
	}
	run = h.s.Store.Runs[a.ID][0]
	if run.Result != automation.Passed || run.WorktreeState != automation.WorktreeKept || run.Changes.Files != 1 || !automation.WorktreePending(run) {
		t.Fatalf("finished run = %+v", run)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(data) != "one\n" {
		t.Fatalf("project changed before merge: %q", data)
	}
	if _, err := h.c.AutomationOp("automationWorktree", automation.Request{ID: a.ID, Started: run.Started, Action: automation.WorktreeMerge}); err != nil {
		t.Fatal(err)
	}
	run = h.s.Store.Runs[a.ID][0]
	if run.WorktreeState != automation.WorktreeMerged || run.WorktreeError != "" {
		t.Fatalf("merged run = %+v", run)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(data) != "fixed\n" {
		t.Fatalf("project after merge: %q", data)
	}
	if w.Exists() || hasProject(h, w.Dir) {
		t.Fatal("worktree left after merge")
	}
	if _, err := h.c.AutomationOp("automationWorktree", automation.Request{ID: a.ID, Started: run.Started, Action: automation.WorktreeDiscard}); err == nil {
		t.Fatal("discarding a merged run succeeded")
	}
}

func TestAutomationWorktreeWithoutChangesIsCleaned(t *testing.T) {
	h := newAutomationHarness(t)
	repo := gitProject(t, h)
	a := h.add(t, automation.Automation{Name: "Look", Instructions: "Look around", Schedule: "0 9 * * *", Worktree: true})
	if err := h.s.StartRun(a.ID, false); err != nil {
		t.Fatal(err)
	}
	run, _ := h.s.Store.LastRun(a.ID)
	w := *run.Worktree
	h.endTurn(run.Thread, "Nothing to do")
	h.s.Tick()
	run, _ = h.s.Store.LastRun(a.ID)
	if run.Result != automation.Passed || run.WorktreeState != automation.WorktreeCleaned || w.Exists() || hasProject(h, w.Dir) {
		t.Fatalf("run = %+v, exists %v", run, w.Exists())
	}
	if branches := gitRun(t, repo, "branch", "--list", "miya/*"); branches != "" {
		t.Fatalf("branches left: %q", branches)
	}
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

func TestAutomationPausedWorktreeRunNowCompletes(t *testing.T) {
	h := newAutomationHarness(t)
	gitProject(t, h)
	a := h.add(t, automation.Automation{Name: "Manual check", Instructions: "Reply done", Schedule: "0 9 1 1 *", Worktree: true})
	h.s.Store.Automations[0].Enabled = false
	if err := h.s.StartRun(a.ID, false); err != nil {
		t.Fatal(err)
	}
	run := h.last(a.ID)
	if run.Worktree == nil || run.Thread == 0 {
		t.Fatalf("worktree run did not start: %+v", run)
	}
	h.endTurn(run.Thread, "Done")
	h.s.Tick()
	if run = h.last(a.ID); run.Result != automation.Passed || run.WorktreeState != automation.WorktreeCleaned || h.s.Enabled() {
		t.Fatalf("paused manual worktree run did not finish and clean up: %+v", run)
	}
}
