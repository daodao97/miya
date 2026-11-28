package nsui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	projectmodel "miya/internal/project/model"
	"miya/internal/uiruntime"
)

func TestSidebarExpandCollapseAlignsWithConversationIcon(t *testing.T) {
	for _, layout := range []string{"project", "standalone"} {
		t.Run(layout, func(t *testing.T) {
			h := projectHost()
			if layout == "standalone" {
				h.state.Projects = nil
			}
			for id := len(h.threads) + 1; id <= msgfmt.CollapsedThreadCount+1; id++ {
				h.threads = append(h.threads, model.Thread{ID: id, Title: "Conversation " + strconv.Itoa(id)})
			}
			app := startDesktop(t, h)
			last := "id:thread-" + strconv.Itoa(len(h.threads))
			for _, state := range []struct {
				label    string
				expanded bool
			}{{"Expand", false}, {"Collapse", true}} {
				row := app.Find("id:thread-1")
				icon := row.Children[0].Children[0]
				toggle := app.Find("id:more-threads").Children[0].Children[0]
				if toggle.Text != tr(state.label) || app.Exists(last) != state.expanded {
					t.Fatalf("unexpected conversation list for %q", state.label)
				}
				if labelX, iconX := toggle.Bounds().Origin.X, icon.Bounds().Origin.X; labelX != iconX {
					t.Fatalf("%s label starts at %v, conversation icon starts at %v", state.label, labelX, iconX)
				}
				app.Press("id:more-threads")
				app.Settle()
			}
			if app.Exists(last) {
				t.Fatal("older conversation still shown after collapsing")
			}
		})
	}
}

func projectHost() *fakeHost {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"}, model.Thread{ID: 2, Title: "Beta"})
	h.state.Projects = func() []projectmodel.Project {
		return []projectmodel.Project{{Name: "miya", Path: "/workspace"}, {Name: "other", Path: "/other"}}
	}
	h.state.ProjectCache = map[string][]model.Thread{"/other": {{ID: 9, Title: "Done elsewhere", LastActivityAt: 5}}}
	h.unread = map[int]bool{9: true}
	return h
}

func TestSidebarKeepsRowsWhenSelectingConversation(t *testing.T) {
	h := projectHost()
	h.unread[2] = true
	app := startDesktop(t, h)
	alpha, beta := app.Find("id:thread-1").Children[0].Children[0].ID, app.Find("id:thread-2").Children[0].Children[0].ID
	project := app.Find("id:project-/workspace").Children[0].Children[0].ID
	agentIcon := sidebarTestGlyph(app.Find("id:picker-agent"))
	permissionIcon := sidebarTestGlyph(app.Find("id:picker-permissions"))
	if agentIcon == nil || permissionIcon == nil {
		t.Fatal("composer icons missing")
	}
	attach := app.Find("id:attach-files").ID
	card := app.Find("id:composer-card").ID
	app.Press("id:thread-2")
	app.Settle()
	if h.current != 2 {
		t.Fatalf("current = %d, want 2", h.current)
	}
	for _, loading := range []bool{true, false} {
		app.Post(func() {
			delete(h.unread, 2)
			h.threads[1].Loading = loading
			h.threads[1].Title = "Beta loaded"
			h.threads[1].LastActivityAt++
			uiruntime.Invalidate()
		})
		app.Settle()
		if app.Find("id:thread-1").Children[0].Children[0].ID != alpha ||
			app.Find("id:thread-2").Children[0].Children[0].ID != beta ||
			app.Find("id:project-/workspace").Children[0].Children[0].ID != project {
			t.Fatalf("sidebar icons were rebuilt with loading = %t", loading)
		}
		if app.Find("id:composer-card").ID != card || sidebarTestGlyph(app.Find("id:picker-agent")) != agentIcon ||
			sidebarTestGlyph(app.Find("id:picker-permissions")) != permissionIcon || app.Find("id:attach-files").ID != attach {
			t.Fatalf("composer controls were rebuilt with loading = %t", loading)
		}
	}
	if app.Exists("id:activity-2") || app.Find("id:thread-2").Children[0].Children[1].Text != "Beta loaded" {
		t.Fatal("sidebar did not show the updated thread state")
	}
}

func sidebarTestGlyph(v *nsgotest.View) *nsgotest.View {
	if v.Kind == nsgotest.KindCanvas || v.Kind == nsgotest.KindImage {
		return v
	}
	for _, child := range v.Children {
		if glyph := sidebarTestGlyph(child); glyph != nil {
			return glyph
		}
	}
	return nil
}

type sidebarActionHost struct {
	*fakeHost
	archived int
}

func (h *sidebarActionHost) ArchiveThread(t model.Thread) { h.archived = t.ID }

func TestSidebarHoverActionsKeepMenuOpen(t *testing.T) {
	h := &sidebarActionHost{fakeHost: projectHost()}
	h.threads[1].Title = strings.Repeat("Long conversation title ", 8)
	app := startDesktop(t, h)
	const session = "id:actions-thread-2"
	const project = "id:actions-project-/workspace"
	const newThread = "id:new-project-/workspace"
	if app.Exists(session) || app.Exists(project) || app.Exists(newThread) {
		t.Fatal("row actions shown without hover")
	}
	threadTitle := func() *nsgotest.View { return app.Find("id:thread-2").Children[0].Children[1] }
	resting := threadTitle().Bounds()
	app.Hover("id:thread-2")
	if hovered := threadTitle().Bounds(); hovered != resting {
		t.Fatalf("conversation title moved or resized on hover: %v to %v", resting, hovered)
	}
	action := app.Find(session)
	button := action.Bounds()
	if button.Origin.X >= resting.Origin.X+resting.Size.Width {
		t.Fatalf("conversation action at %v does not overlay the title at %v", button, resting)
	}
	backing := action.Parent.Parent
	cover := backing.Bounds()
	if backing.Style.Background != app.Find("id:window").Style.Background ||
		action.Children[0].Style.Background != 0 || backing.Style.Rounded() || cover.Origin.X >= button.Origin.X ||
		cover.Origin.X+cover.Size.Width < resting.Origin.X+resting.Size.Width ||
		cover.Origin.Y > resting.Origin.Y || cover.Origin.Y+cover.Size.Height < resting.Origin.Y+resting.Size.Height {
		t.Fatalf("action backing at %v with color %v does not match the sidebar or cover the title behind the button at %v", cover, backing.Style.Background, button)
	}
	app.Press(session)
	app.HoverOut()
	if !app.Exists("label:Archive conversation") || h.current != 1 {
		t.Fatal("conversation menu closed or selected the row")
	}
	app.Press("label:Archive conversation")
	if h.archived != 2 {
		t.Fatalf("archived conversation %d, want 2", h.archived)
	}
	app.Hover("id:project-/workspace")
	if !app.Exists(project) || !app.Exists(newThread) {
		t.Fatal("project hover actions missing")
	}
	app.Press(project)
	app.HoverOut()
	if !app.Exists("label:Archive project") || !app.Exists("id:thread-1") {
		t.Fatal("project menu closed or collapsed the project")
	}
	app.Key("escape")
	app.Hover("id:project-/workspace")
	app.Press(newThread)
	if h.current != 0 || h.state.Cwd != "/workspace" {
		t.Fatalf("new conversation opened in %q with id %d", h.state.Cwd, h.current)
	}
}

func TestSidebarLoadingIndicatorFloats(t *testing.T) {
	h := projectHost()
	h.threads[0].Title = strings.Repeat("Long conversation title ", 8)
	app := startDesktop(t, h)
	const rowID = "id:thread-1"
	const spinnerID = "id:spinner-thread-1"
	content := func() []*nsgotest.View { return app.Find(rowID).Children[0].Children }
	withinRow := func(view *nsgotest.View) core.Bounds {
		b, row := view.Bounds(), app.Find(rowID).Bounds()
		b.Origin.X -= row.Origin.X
		b.Origin.Y -= row.Origin.Y
		return b
	}
	icon, title := withinRow(content()[0]), withinRow(content()[1])
	for _, loading := range []bool{true, false} {
		app.Post(func() {
			h.threads[0].Loading = loading
			h.threads[0].Busy = !loading
			uiruntime.Invalidate()
		})
		app.Settle()
		spinner := app.Find(spinnerID)
		if withinRow(content()[0]) != icon || withinRow(content()[1]) != title {
			t.Fatalf("spinner changed the row layout with loading = %t", loading)
		}
		backing := spinner.Parent.Parent
		rowFill := app.Find(rowID).Style.Background.Over(app.Find("id:window").Style.Background)
		titleBounds := content()[1].Bounds()
		if backing.Style.Background != rowFill || backing.Bounds().Origin.X >= titleBounds.Origin.X+titleBounds.Size.Width {
			t.Fatalf("spinner backing does not overlay the title or match the selected row")
		}
	}
	app.Hover(rowID)
	if app.Exists(spinnerID) || !app.Exists("id:actions-thread-1") || withinRow(content()[1]) != title {
		t.Fatal("hover should show the action in the indicator's place without resizing the title")
	}
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
