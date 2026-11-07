package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo is a repository with one commit, isolated from the person's Git
// configuration.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Miya Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.invalid")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "demo")
	run(t, "", "init", "-q", "-b", "main", repo)
	write(t, filepath.Join(repo, "a.txt"), "one\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "init")
	return repo
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreateIsolatesWorkAndCleansUp(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	root := filepath.Join(filepath.Dir(repo), "worktrees")
	w, err := Create(ctx, filepath.Join(repo), root, "auto", "Nightly Check")
	if err != nil {
		t.Fatal(err)
	}
	if w.Repo != repo || w.Branch != "miya/auto/nightly-check" || filepath.Base(w.Dir) != "demo-nightly-check" || !w.Exists() {
		t.Fatalf("worktree = %+v", w)
	}
	if c, err := w.Changes(ctx); err != nil || !c.Empty() {
		t.Fatalf("fresh changes = %+v, %v", c, err)
	}
	write(t, filepath.Join(w.Dir, "a.txt"), "two\n")
	write(t, filepath.Join(w.Dir, "new.txt"), "new\n")
	if c, _ := w.Changes(ctx); c.Files != 2 || c.Commits != 0 {
		t.Fatalf("changes = %+v", c)
	}
	// The project's checkout does not see the work.
	if data, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(data) != "one\n" {
		t.Fatalf("project changed: %q", data)
	}
	// A second worktree of the same name gets a free one.
	w2, err := Create(ctx, repo, root, "auto", "Nightly Check")
	if err != nil || w2.Branch != "miya/auto/nightly-check-2" {
		t.Fatalf("second = %+v, %v", w2, err)
	}
	for _, x := range []Worktree{w, w2} {
		if err := x.Remove(ctx, false); err != nil {
			t.Fatal(err)
		}
		if x.Exists() {
			t.Fatalf("%s still exists", x.Dir)
		}
	}
	if branches := run(t, repo, "branch", "--list", "miya/*"); branches != "" {
		t.Fatalf("branches left: %q", branches)
	}
}

func TestMergeBringsWorkIntoProject(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	w, err := Create(ctx, repo, filepath.Join(filepath.Dir(repo), "wt"), "auto", "fix")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(w.Dir, "b.txt"), "b\n")
	if err := w.Merge(ctx, "Miya: fix"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "b.txt")); string(data) != "b\n" {
		t.Fatalf("merged file = %q", data)
	}
	if err := w.Remove(ctx, true); err != nil {
		t.Fatal(err)
	}
	if run(t, repo, "branch", "--list", w.Branch) == "" {
		t.Fatal("kept branch was deleted")
	}
}

func TestMergeConflictLeavesProjectAsItWas(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	w, err := Create(ctx, repo, filepath.Join(filepath.Dir(repo), "wt"), "auto", "clash")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(w.Dir, "a.txt"), "from worktree\n")
	write(t, filepath.Join(repo, "a.txt"), "from project\n")
	run(t, repo, "commit", "-q", "-am", "project edit")
	if err := w.Merge(ctx, "Miya: clash"); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("merge err = %v", err)
	}
	if status := run(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("project left mid-merge: %q", status)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(data) != "from project\n" {
		t.Fatalf("project file = %q", data)
	}
}

func TestRepoRejectsNonRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := Repo(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("err = %v", err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Nightly Check": "nightly-check", "每日检查": "run", "  a/b..c ": "a-b..c"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
