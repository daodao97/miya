package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"miya/locales"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// testRepo has a commit of a.txt, a staged line, an unstaged line and an
// untracked file.
func testRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	runGit(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "a.txt"), "one\n")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "initial")
	writeFile(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")
	runGit(t, dir, "add", "a.txt")
	writeFile(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	writeFile(t, filepath.Join(dir, "c.txt"), "new\n")
}

func TestCollectDiffSources(t *testing.T) {
	dir := t.TempDir()
	testRepo(t, dir)
	ctx := context.Background()
	stats := func(src DiffSource) map[string][2]int {
		snap, err := CollectDiff(ctx, dir, src)
		if err != nil {
			t.Fatalf("%s: %v", DiffSourceNames()[src], err)
		}
		out := map[string][2]int{}
		for _, f := range snap.Files {
			out[f.Path] = [2]int{f.Added, f.Deleted}
		}
		return out
	}
	for src, want := range map[DiffSource]map[string][2]int{
		SourceUncommitted: {"a.txt": {2, 0}, "c.txt": {1, 0}},
		SourceUnstaged:    {"a.txt": {1, 0}, "c.txt": {1, 0}},
		SourceStaged:      {"a.txt": {1, 0}},
		SourceBranch:      {"a.txt": {2, 0}, "c.txt": {1, 0}},
	} {
		got := stats(src)
		if len(got) != len(want) {
			t.Fatalf("%s: files %v, want %v", DiffSourceNames()[src], got, want)
		}
		for path, n := range want {
			if got[path] != n {
				t.Errorf("%s: %s %v, want %v", DiffSourceNames()[src], path, got[path], n)
			}
		}
	}
	snap, _ := CollectDiff(ctx, dir, SourceStaged)
	old, new := FileSides(ctx, dir, snap, snap.Files[0])
	if old != "one\n" || new != "one\ntwo\n" {
		t.Fatalf("staged sides %q → %q", old, new)
	}
	snap, _ = CollectDiff(ctx, dir, SourceUnstaged)
	for _, f := range snap.Files {
		old, new := FileSides(ctx, dir, snap, f)
		if f.Path == "a.txt" && (old != "one\ntwo\n" || new != "one\ntwo\nthree\n") {
			t.Fatalf("unstaged sides %q → %q", old, new)
		}
		if f.Path == "c.txt" && (!f.Untracked || old != "" || new != "new\n") {
			t.Fatalf("untracked sides %+v %q → %q", f, old, new)
		}
	}
	if _, err := CollectDiff(ctx, t.TempDir(), SourceUncommitted); err == nil {
		t.Fatal("a folder outside Git has no review")
	}
}

func TestDiffSourceNamesFollowTheInterfaceLanguage(t *testing.T) {
	previous := locales.Current()
	defer locales.SetCurrent(previous)
	locales.SetCurrent("zh-Hans")
	if got := DiffSourceNames()[SourceUncommitted]; got != "未提交的更改" {
		t.Fatalf("Chinese name: %q", got)
	}
	locales.SetCurrent("en")
	if got := DiffSourceNames()[SourceUncommitted]; got != "Uncommitted changes" {
		t.Fatalf("English name: %q", got)
	}
}
