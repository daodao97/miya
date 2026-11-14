package nsui

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/chat/model"
	projectfiles "miya/internal/project/files"
	projectgit "miya/internal/project/git"
)

// fakeFiles is a workspace in memory. Its reads run on the panel's
// goroutines, so it locks.
type fakeFiles struct {
	mu      sync.Mutex
	dirs    map[string][]projectfiles.FileEntry
	texts   map[string]string
	snap    projectgit.DiffSnapshot
	sides   map[string][2]string
	sources []projectgit.DiffSource
	read    []string
}

func (f *fakeFiles) ReadDir(root, rel string) ([]projectfiles.FileEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dirs[rel], nil
}

func (f *fakeFiles) ReadImage(root, rel string) ([]byte, error) { return nil, os.ErrNotExist }

func (f *fakeFiles) ReadText(root, rel string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text, ok := f.texts[rel]
	if !ok {
		return "", os.ErrNotExist
	}
	return text, nil
}

func (f *fakeFiles) Diff(ctx context.Context, root string, src projectgit.DiffSource) (projectgit.DiffSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sources = append(f.sources, src)
	snap := f.snap
	snap.Source = src
	return snap, nil
}

func (f *fakeFiles) Sides(ctx context.Context, root string, snap projectgit.DiffSnapshot, file projectgit.ChangedFile) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.read = append(f.read, file.Path)
	s := f.sides[file.Path]
	return s[0], s[1], nil
}

// Terminal fails, so tests never start a shell.
func (f *fakeFiles) Terminal(string) (io.ReadWriteCloser, error) {
	return nil, errors.New("no terminal in tests")
}

func (f *fakeFiles) sidesRead() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.read...)
}

func newPanelFiles() *fakeFiles {
	return &fakeFiles{
		dirs: map[string][]projectfiles.FileEntry{
			"":    {{Name: "src", Rel: "src", Dir: true}, {Name: "notes.txt", Rel: "notes.txt"}},
			"src": {{Name: "main.go", Rel: "src/main.go"}},
		},
		texts: map[string]string{"notes.txt": "remember the milk\n", "src/main.go": "package main\n"},
		snap: projectgit.DiffSnapshot{Added: 1, Deleted: 1, Files: []projectgit.ChangedFile{
			{Path: "src/main.go", Added: 1, Deleted: 1},
			{Path: "logo.png", Binary: true},
		}},
		sides: map[string][2]string{"src/main.go": {"package main\n", "package app\n"}},
	}
}

func startPanel(t *testing.T) (*nsgotest.App, *fakeFiles) {
	t.Helper()
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"})
	files := newPanelFiles()
	h.files = files
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Settle()
	return app, files
}

func joinedTexts(app *nsgotest.App) string { return strings.Join(app.Texts(), "\n") }

// The header's toggle opens the panel on its cards; a card opens its tab,
// and the files tab browses the workspace into a file.
func TestPanelOpensFilesFromShell(t *testing.T) {
	app, _ := startPanel(t)
	if !app.Exists("id:panel-chooser") || !app.Exists("id:panel-open-review") {
		t.Fatalf("panel cards not shown; texts %q", app.Texts())
	}
	app.Press("id:panel-open-files")
	app.WaitFor("id:file-src", time.Second)
	if app.Exists("id:panel-chooser") || !app.Exists("id:panel-tab-1") {
		t.Fatal("files tab did not replace the cards")
	}
	if app.Exists("id:file-src/main.go") {
		t.Fatal("folder shows its files before it is expanded")
	}
	app.Press("id:file-src")
	app.WaitFor("id:file-src/main.go", time.Second)
	app.Press("id:file-src/main.go")
	app.WaitFor("id:file-editor", time.Second)
	if got := app.Find("id:panel-tab-label-1").Text; got != "main.go" {
		t.Fatalf("tab label = %q, want the open file's name", got)
	}
	app.Press("id:tool-arrow-left")
	app.WaitFor("id:file-tree", time.Second)
	if got := app.Find("id:panel-tab-label-1").Text; got != "Files" {
		t.Fatalf("tab label after going back = %q, want Files", got)
	}
}

// Tabs show their own surfaces; closing the shown one shows its neighbour.
func TestPanelSwitchesTabs(t *testing.T) {
	app, _ := startPanel(t)
	app.Press("id:panel-open-files")
	app.WaitFor("id:file-tree", time.Second)
	app.Press("id:panel-add")
	app.Settle()
	app.Press("id:panel-add-review")
	app.WaitFor("id:review", time.Second)
	if app.Exists("id:file-tree") {
		t.Fatal("files still shown beside the review")
	}
	app.Press("id:panel-tab-1")
	app.WaitFor("id:file-tree", time.Second)
	if app.Exists("id:review") {
		t.Fatal("review still shown after choosing the files tab")
	}
	app.Press("id:panel-tab-2")
	app.WaitFor("id:review", time.Second)
	app.Press("id:panel-close-2")
	app.WaitFor("id:file-tree", time.Second)
	if app.Exists("id:panel-tab-2") {
		t.Fatal("closed tab still listed")
	}
	app.Press("id:panel-close-1")
	app.WaitFor("id:panel-chooser", time.Second)
}

// The review lists the changed files and shows the first one's diff; a
// binary file says it has none.
func TestPanelShowsDiff(t *testing.T) {
	app, files := startPanel(t)
	app.Press("id:panel-open-review")
	app.WaitFor("id:review-diff", time.Second)
	texts := joinedTexts(app)
	for _, want := range []string{"main.go", "logo.png", "2 files", "+1", "package app"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("review missing %q; texts %q", want, app.Texts())
		}
	}
	if got := files.sidesRead(); len(got) != 1 || got[0] != "src/main.go" {
		t.Fatalf("sides read %q, want src/main.go", got)
	}
	app.Press("id:changed-logo.png")
	app.WaitForGone("id:review-diff", time.Second)
	if !strings.Contains(joinedTexts(app), "Binary file; no diff available") {
		t.Fatalf("binary note missing; texts %q", app.Texts())
	}
	app.Press("id:changed-src/main.go")
	app.WaitFor("id:review-diff", time.Second)
}

// A new workspace drops what the files read and reads the new one.
func TestPanelFollowsWorkspace(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"})
	files := newPanelFiles()
	h.files = files
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Settle()
	app.Press("id:panel-open-files")
	app.WaitFor("id:file-notes.txt", time.Second)
	files.mu.Lock()
	files.dirs = map[string][]projectfiles.FileEntry{"": {{Name: "other.md", Rel: "other.md"}}}
	files.mu.Unlock()
	app.Post(func() { _ = h.ChangeWorkspace("/elsewhere") })
	app.WaitFor("id:file-other.md", time.Second)
	if app.Exists("id:file-notes.txt") {
		t.Fatal("old workspace's files still listed")
	}
}

func TestBrowserURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                "",
		"3000":            "http://localhost:3000",
		"localhost:8080":  "http://localhost:8080",
		"example.com":     "https://example.com",
		"http://a.test/x": "http://a.test/x",
		"about:blank":     "about:blank",
	} {
		if got := browserURL(in); got != want {
			t.Errorf("browserURL(%q) = %q, want %q", in, got, want)
		}
	}
}
