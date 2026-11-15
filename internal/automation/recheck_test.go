package automation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/project/worktree"
)

// gitRepo makes a repository with one commit, ignoring ignored.txt.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Miya Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.com")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

// The recheck sees what a merge would bring, uncommitted files included,
// and nothing git ignores.
func TestRecheckRunsTheCheckOnOnlyTheChanges(t *testing.T) {
	repo := gitRepo(t)
	ctx := context.Background()
	wt, err := worktree.Create(ctx, repo, t.TempDir(), "automation", "recheck")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"made.txt": "work", "ignored.txt": "local only"} {
		if err := os.WriteFile(filepath.Join(wt.Dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, out := recheckWorktree(ctx, wt, "test -f made.txt", runCheck); got != RecheckPassed {
		t.Fatalf("an uncommitted change was not in the checkout: %s %s", got, out)
	}
	if got, _ := recheckWorktree(ctx, wt, "test -f ignored.txt", runCheck); got != RecheckFailed {
		t.Fatal("an ignored file was in the clean checkout")
	}
	// The checkouts are gone; the worktree and its work stay.
	list, _ := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if n := countLines(string(list), "worktree "); n != 2 {
		t.Fatalf("worktrees left: %d\n%s", n, list)
	}
	if _, err := os.Stat(filepath.Join(wt.Dir, "made.txt")); err != nil {
		t.Fatal("the run's work is gone")
	}
	label := WorktreeLabel(Run{Worktree: &wt, WorktreeState: WorktreeKept, Recheck: RecheckFailed})
	if label == "" {
		t.Fatal("no label")
	}
	views := []View{{Automation: Automation{ID: "a", Name: "Deps", Enabled: true}, Runs: []Run{{Result: Passed, Worktree: &wt, WorktreeState: WorktreeKept,
		Changes: worktree.Changes{Commits: 1}, Recheck: RecheckFailed, RecheckOutput: "ok  a\nFAIL b: missing module"}}}}
	if got := NeedsAttention(views); len(got) != 1 || got[0].Kind != AttentionRecheck || got[0].Detail != "FAIL b: missing module" {
		t.Fatalf("attention: %+v", got)
	}
}

func countLines(s, prefix string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}
