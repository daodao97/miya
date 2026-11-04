package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// syncPanel runs the panel's disk and git work in place, so a frame after a
// click shows its result.
func syncPanel(t *testing.T) {
	t.Helper()
	old := goAsync
	goAsync = func(f func()) { f() }
	t.Cleanup(func() { goAsync = old })
}

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

func TestBrowserURL(t *testing.T) {
	for in, want := range map[string]string{
		"3000":                 "http://localhost:3000",
		"localhost:5173/docs":  "http://localhost:5173/docs",
		"127.0.0.1:8080":       "http://127.0.0.1:8080",
		"example.com":          "https://example.com",
		"http://example.com/a": "http://example.com/a",
		"  https://go.dev  ":   "https://go.dev",
		"about:blank":          "about:blank",
		"":                     "",
	} {
		if got := browserURL(in); got != want {
			t.Errorf("browserURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollectDiffSources(t *testing.T) {
	dir := t.TempDir()
	testRepo(t, dir)
	ctx := context.Background()
	stats := func(src diffSource) map[string][2]int {
		snap, err := collectDiff(ctx, dir, src)
		if err != nil {
			t.Fatalf("%s: %v", diffSourceNames[src], err)
		}
		out := map[string][2]int{}
		for _, f := range snap.Files {
			out[f.Path] = [2]int{f.Added, f.Deleted}
		}
		return out
	}
	for src, want := range map[diffSource]map[string][2]int{
		sourceUncommitted: {"a.txt": {2, 0}, "c.txt": {1, 0}},
		sourceUnstaged:    {"a.txt": {1, 0}, "c.txt": {1, 0}},
		sourceStaged:      {"a.txt": {1, 0}},
		sourceBranch:      {"a.txt": {2, 0}, "c.txt": {1, 0}},
	} {
		got := stats(src)
		if len(got) != len(want) {
			t.Fatalf("%s: files %v, want %v", diffSourceNames[src], got, want)
		}
		for path, n := range want {
			if got[path] != n {
				t.Errorf("%s: %s %v, want %v", diffSourceNames[src], path, got[path], n)
			}
		}
	}
	snap, _ := collectDiff(ctx, dir, sourceStaged)
	old, new := fileSides(ctx, dir, snap, snap.Files[0])
	if old != "one\n" || new != "one\ntwo\n" {
		t.Fatalf("staged sides %q → %q", old, new)
	}
	snap, _ = collectDiff(ctx, dir, sourceUnstaged)
	for _, f := range snap.Files {
		old, new := fileSides(ctx, dir, snap, f)
		if f.Path == "a.txt" && (old != "one\ntwo\n" || new != "one\ntwo\nthree\n") {
			t.Fatalf("unstaged sides %q → %q", old, new)
		}
		if f.Path == "c.txt" && (!f.Untracked || old != "" || new != "new\n") {
			t.Fatalf("untracked sides %+v %q → %q", f, old, new)
		}
	}
	if _, err := collectDiff(ctx, t.TempDir(), sourceUncommitted); err == nil {
		t.Fatal("a folder outside Git has no review")
	}
}

func TestPanelOpensFilesEditsAndSaves(t *testing.T) {
	syncPanel(t)
	app, tt := testApp(t)
	cwd := app.c.cwd
	writeFile(t, filepath.Join(cwd, "README.md"), "# Demo\n")
	writeFile(t, filepath.Join(cwd, "src", "main.go"), "package main\n")
	if err := tt.Click("Show right panel"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Open a panel") || !tt.HasText("Open a terminal in this workspace") {
		t.Fatalf("chooser missing: %q", tt.Texts())
	}
	if err := tt.Click("Files"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("src") || !tt.HasText("README.md") {
		t.Fatalf("tree missing: %q", tt.Texts())
	}
	if err := tt.Click("src"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("main.go"); err != nil {
		t.Fatalf("expanded folder has no main.go in %q: %v", tt.Texts(), err)
	}
	p := app.panel()
	ed := p.editors["src/main.go"]
	if p.selected != "src/main.go" || ed == nil || ed.state == nil {
		t.Fatalf("file not opened: %q %+v", p.selected, ed)
	}
	ed.state.Buffer.Set("package main\n\n// edited\n")
	tt.Frame()
	if !p.dirty(p.surfaces[p.active]) {
		t.Fatal("edited file not marked unsaved")
	}
	if err := tt.Click("Save (⌘S)"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(cwd, "src", "main.go"))
	if string(data) != "package main\n\n// edited\n" || ed.state.Buffer.Modified() {
		t.Fatalf("save wrote %q", data)
	}
	if err := tt.Click("Back to file list"); err != nil {
		t.Fatal(err)
	}
	if p.selected != "" || !tt.HasText("README.md") {
		t.Fatal("did not return to the tree")
	}
	if err := tt.Click("Hide right panel"); err != nil {
		t.Fatal(err)
	}
	if p.visible || tt.HasText("README.md") {
		t.Fatal("panel still shown")
	}
}

func TestPanelReviewsChanges(t *testing.T) {
	syncPanel(t)
	app, tt := testApp(t)
	testRepo(t, app.c.cwd)
	if err := tt.Click("Show right panel"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Review"); err != nil {
		t.Fatal(err)
	}
	p := app.panel()
	if p.diff == nil || len(p.diff.Files) != 2 {
		t.Fatalf("review not read: %+v %s", p.diff, p.diffErr)
	}
	if !tt.HasText("a.txt") || !tt.HasText("c.txt") || !tt.HasText("New file") || !tt.HasText("2 files") {
		t.Fatalf("changed files missing: %q", tt.Texts())
	}
	if p.diffView == nil || p.diffViewPath != "a.txt" || p.diffView.New.String() != "one\ntwo\nthree\n" {
		t.Fatalf("first file's diff not shown: %q", p.diffViewPath)
	}
	if err := tt.Click("c.txt"); err != nil {
		t.Fatal(err)
	}
	if p.diffViewPath != "c.txt" || p.diffView.Old.String() != "" {
		t.Fatal("untracked file's diff not shown")
	}
}

func TestPanelBelongsToItsConversation(t *testing.T) {
	syncPanel(t)
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Title: "First thread", HistoryLoaded: true, Agent: "claude-code"}, {ID: 2, Title: "Second thread", HistoryLoaded: true, Agent: "claude-code"}})
	c.setActive(1)
	tt.Frame()
	if err := tt.Click("Show right panel"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Files"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Second thread"); err != nil {
		t.Fatal(err)
	}
	if p := app.panel(); len(p.surfaces) != 0 || !p.visible || !tt.HasText("Open a panel") {
		t.Fatalf("second conversation shares the first's tabs: %+v", p.surfaces)
	}
	if err := tt.Click("First thread"); err != nil {
		t.Fatal(err)
	}
	if p := app.panel(); len(p.surfaces) != 1 || p.surfaces[0].kind != surfaceFiles {
		t.Fatal("first conversation lost its tab")
	}
	// Closing the last tab shows the chooser again.
	if err := tt.Click("CloseFiles"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Open a panel") {
		t.Fatalf("chooser not back: %q", tt.Texts())
	}
}
