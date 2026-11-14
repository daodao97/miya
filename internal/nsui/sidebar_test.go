package nsui

import (
	"testing"
	"time"

	"miya/internal/chat/model"
	projectmodel "miya/internal/project/model"
)

func projectHost() *fakeHost {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"}, model.Thread{ID: 2, Title: "Beta"})
	h.state.Projects = func() []projectmodel.Project {
		return []projectmodel.Project{{Name: "miya", Path: "/workspace"}, {Name: "other", Path: "/other"}}
	}
	h.state.ProjectCache = map[string][]model.Thread{"/other": {{ID: 9, Title: "Done elsewhere", LastActivityAt: 5}}}
	h.unread = map[int]bool{9: true}
	return h
}

// The open project lists its conversations under it; a completed
// conversation the user has not seen shows in the activity from any project.
func TestSidebarProjectsAndActivity(t *testing.T) {
	h := projectHost()
	app := startBridged(t, h)
	for _, id := range []string{"id:project-/workspace", "id:project-/other", "id:thread-1", "id:thread-2", "id:activity-9"} {
		if !app.Exists(id) {
			t.Fatalf("%s missing; texts %q", id, app.Texts())
		}
	}
	app.Press("id:activity-9")
	app.Settle()
	if h.current != 9 {
		t.Fatalf("current = %d, want 9", h.current)
	}
	// Choosing the open project again folds its conversations away.
	app.Press("id:project-/workspace")
	app.Settle()
	if app.Exists("id:thread-1") {
		t.Fatal("open project's conversations still listed after folding it")
	}
}

// A project needs a name before the folder is opened.
func TestProjectDialogNeedsName(t *testing.T) {
	h := projectHost()
	app := startBridged(t, h)
	app.Press("label:Select project")
	app.Settle()
	if !app.Exists("id:project-dialog") {
		t.Fatalf("project dialog not shown; texts %q", app.Texts())
	}
	app.Press("id:project-confirm")
	app.Settle()
	if !app.Exists("id:project-error") {
		t.Fatal("no error for a project without a name")
	}
	// The folder starts as the open one.
	app.Type("label:Project name", "scratch")
	app.Press("id:project-confirm")
	app.Settle()
	if app.Exists("id:project-error") || app.Exists("id:project-confirm") || h.state.Cwd != "/workspace" {
		t.Fatalf("project not opened: cwd %q; texts %q", h.state.Cwd, app.Texts())
	}
}

// Browse chooses the folder in the picker and names the project after it.
func TestProjectDialogBrowses(t *testing.T) {
	dir := t.TempDir()
	h := projectHost()
	h.state.Cwd = dir
	app := startBridged(t, h)
	app.Press("label:Select project")
	app.Settle()
	app.Press("id:project-browse")
	loaded := false
	for range 200 {
		app.Settle()
		if app.Exists("label:Choose") && app.Enabled("label:Choose") {
			loaded = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !loaded {
		t.Fatalf("picker never loaded; texts %q", app.Texts())
	}
	app.Press("label:Choose")
	app.Settle()
	app.Press("id:project-confirm")
	app.Settle()
	if app.Exists("id:project-error") || app.Exists("id:project-confirm") || h.state.Cwd != dir {
		t.Fatalf("browsed project not opened: cwd %q; texts %q", h.state.Cwd, app.Texts())
	}
}
