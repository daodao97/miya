package main

import (
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	desktopui "miya/internal/ui"
	uiruntime "miya/internal/uiruntime"
	os "os"
	exec "os/exec"
	filepath "path/filepath"
	testing "testing"
)

// syncPanel runs the panel's disk and git work in place, so a frame after a
// click shows its result.
func syncPanel(t *testing.T) {
	t.Helper()
	old := uiruntime.GoAsync
	uiruntime.GoAsync = func(f func()) { f() }
	t.Cleanup(func() { uiruntime.GoAsync = old })
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
		if got := desktopui.BrowserURL(in); got != want {
			t.Errorf("browserURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPanelOpensFilesEditsAndSaves(t *testing.T) {
	syncPanel(t)
	app, tt := testApp(t)
	cwd := app.C.(*controller.Controller).Cwd
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
	p := app.Panel()
	ed := p.Editors["src/main.go"]
	if p.Selected != "src/main.go" || ed == nil || ed.State == nil {
		t.Fatalf("file not opened: %q %+v", p.Selected, ed)
	}
	ed.State.Buffer.Set("package main\n\n// edited\n")
	tt.Frame()
	if !p.Dirty(p.Surfaces[p.Active]) {
		t.Fatal("edited file not marked unsaved")
	}
	if err := tt.Click("Save (⌘S)"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(cwd, "src", "main.go"))
	if string(data) != "package main\n\n// edited\n" || ed.State.Buffer.Modified() {
		t.Fatalf("save wrote %q", data)
	}
	if err := tt.Click("Back to file list"); err != nil {
		t.Fatal(err)
	}
	if p.Selected != "" || !tt.HasText("README.md") {
		t.Fatal("did not return to the tree")
	}
	if err := tt.Click("Hide right panel"); err != nil {
		t.Fatal(err)
	}
	if p.Visible || tt.HasText("README.md") {
		t.Fatal("panel still shown")
	}
}

func TestPanelReviewsChanges(t *testing.T) {
	syncPanel(t)
	app, tt := testApp(t)
	testRepo(t, app.C.(*controller.Controller).Cwd)
	if err := tt.Click("Show right panel"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Review"); err != nil {
		t.Fatal(err)
	}
	p := app.Panel()
	if p.Diff == nil || len(p.Diff.Files) != 2 {
		t.Fatalf("review not read: %+v %s", p.Diff, p.DiffErr)
	}
	if !tt.HasText("a.txt") || !tt.HasText("c.txt") || !tt.HasText("New file") || !tt.HasText("2 files") {
		t.Fatalf("changed files missing: %q", tt.Texts())
	}
	if p.DiffView == nil || p.DiffViewPath != "a.txt" || p.DiffView.New.String() != "one\ntwo\nthree\n" {
		t.Fatalf("first file's diff not shown: %q", p.DiffViewPath)
	}
	if err := tt.Click("c.txt"); err != nil {
		t.Fatal(err)
	}
	if p.DiffViewPath != "c.txt" || p.DiffView.Old.String() != "" {
		t.Fatal("untracked file's diff not shown")
	}
}

func TestPanelBelongsToItsConversation(t *testing.T) {
	syncPanel(t)
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]appmodel.Thread{{ID: 1, Title: "First thread", HistoryLoaded: true, Agent: "claude-code"}, {ID: 2, Title: "Second thread", HistoryLoaded: true, Agent: "claude-code"}})
	c.SetActive(1)
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
	if p := app.Panel(); len(p.Surfaces) != 0 || !p.Visible || !tt.HasText("Open a panel") {
		t.Fatalf("second conversation shares the first's tabs: %+v", p.Surfaces)
	}
	if err := tt.Click("First thread"); err != nil {
		t.Fatal(err)
	}
	if p := app.Panel(); len(p.Surfaces) != 1 || p.Surfaces[0].Kind != desktopui.SurfaceFiles {
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
