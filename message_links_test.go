package main

import (
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestLocalFileLink(t *testing.T) {
	cwd := t.TempDir()
	for _, tc := range []struct {
		link, want string
	}{
		{"docs/report.md", "docs/report.md"},
		{"./docs/report.md#results", "docs/report.md"},
		{"docs/report.md:12:3", "docs/report.md"},
		{"docs/report.md:12", "docs/report.md"},
		{"docs/test%20report.md", "docs/test report.md"},
		{filepath.Join(cwd, "docs/report.md"), "docs/report.md"},
		{(&url.URL{Scheme: "file", Path: filepath.Join(cwd, "测试报告.md")}).String(), "测试报告.md"},
		{(&url.URL{Scheme: "file", Host: "localhost", Path: filepath.Join(cwd, "report.md")}).String(), "report.md"},
		{filepath.Join(filepath.Dir(cwd), "outside.md"), "../outside.md"},
	} {
		t.Run(tc.link, func(t *testing.T) {
			got, ok := localFileLink(cwd, tc.link)
			if !ok || got != tc.want {
				t.Fatalf("localFileLink(%q) = %q, %v; want %q", tc.link, got, ok, tc.want)
			}
		})
	}
	for _, link := range []string{"", "#results", "https://example.com/report.md", "mailto:test@example.com", "javascript:alert(1)", "//example.com/report.md", "file://server/report.md", "file:report.md", "docs/%00report.md", "docs/%5creport.md", "docs/%zz.md"} {
		if rel, ok := localFileLink(cwd, link); ok {
			t.Errorf("accepted nonlocal link %q as %q", link, rel)
		}
	}
}

func TestMessageFileLinkCopiesFullPath(t *testing.T) {
	for _, tc := range []struct {
		name, link, file string
	}{
		{"relative", "docs/report.md", "docs/report.md"},
		{"location", "docs/report.md:12:3#results", "docs/report.md"},
		{"escaped", "docs/test%20%E6%8A%A5%E5%91%8A.md", "docs/test 报告.md"},
		{"absolute", "/tmp/report.md:12", "/tmp/report.md"},
		{"file-url", "file:///tmp/test%20report.md", "/tmp/test report.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, tt := testApp(t)
			app.c.setThreads([]Thread{{ID: 1, HistoryLoaded: true, Messages: []Message{{ID: 1, Role: "assistant", Content: "[report.md](" + tc.link + ")"}}}})
			app.c.setActive(1)
			tt.Frame()
			r, ok := tt.Find("report.md")
			if !ok {
				t.Fatal("file link missing")
			}
			tt.RightClickAt(r.X+12, r.Y+r.H/2)
			for _, item := range []string{"Copy path", "Copy", "Select All"} {
				if !slices.Contains(tt.Menu(), item) {
					t.Fatalf("missing %q: %v", item, tt.Menu())
				}
			}
			if err := tt.ChooseMenuItem("Copy path"); err != nil {
				t.Fatal(err)
			}
			want := tc.file
			if !filepath.IsAbs(want) {
				want = filepath.Join(app.c.cwd, want)
			}
			if got := tt.Clipboard(); got != want {
				t.Fatalf("copied %q, want %q", got, want)
			}
		})
	}
}

func TestMobileMessageFileLinkCopiesDesktopPath(t *testing.T) {
	m := testMobileApp()
	th := m.state.Projects[1].Threads[0]
	th.HistoryLoaded = true
	th.Messages = []Message{{ID: 1, Role: "assistant", Content: "[report.md](docs/report.md:42)"}}
	m.state.Projects[1].Threads[0] = th
	m.selected = "/a"
	m.openThread(th)
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	r, ok := tt.Find("report.md")
	if !ok {
		t.Fatal("file link missing")
	}
	tt.RightClickAt(r.X+12, r.Y+r.H/2)
	if err := tt.ChooseMenuItem("Copy path"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != "/b/docs/report.md" {
		t.Fatalf("copied %q, want the open conversation's desktop path", got)
	}
}

func TestMessageFileLinkMenusAcrossRenderers(t *testing.T) {
	for _, kind := range []string{"stream", "reference-paragraph", "reference-inline"} {
		t.Run(kind, func(t *testing.T) {
			app, _ := testApp(t)
			source := handoffSource()
			app.c.setThreads([]Thread{source})
			message := Message{Role: "assistant", Content: "[report.md](docs/report.md)"}
			if kind != "stream" {
				message.Role = "user"
				separator := "\n\n"
				if kind == "reference-inline" {
					separator = " "
				}
				message.Content += separator + referenceText(testReference(source), source.Title)
			}
			tt := ui.NewTester(func(c *ui.Context) {
				app.md.LinkMenu = messageLinkMenu(c, app.c.cwd)
				app.messageView(c, message, kind == "stream")
			}, 800, 400)
			tt.Advance(3 * time.Second)
			tt.Frame()
			r, ok := tt.Find("report.md")
			if !ok {
				t.Fatal("file link missing")
			}
			tt.RightClickAt(r.X+12, r.Y+r.H/2)
			if err := tt.ChooseMenuItem("Copy path"); err != nil {
				t.Fatal(err)
			}
			if got, want := tt.Clipboard(), filepath.Join(app.c.cwd, "docs/report.md"); got != want {
				t.Fatalf("copied %q, want %q", got, want)
			}
		})
	}
}

func TestReportLinkOpensFilesPanel(t *testing.T) {
	syncPanel(t)
	app, tt := testApp(t)
	app.md.OnLink = app.openMessageLink
	app.c.setThreads([]Thread{{ID: 1, Title: "Report", HistoryLoaded: true, Messages: []Message{{ID: 1, Role: "assistant", Content: "[查看测试报告](docs/report.md)"}}}})
	app.c.setActive(1)
	writeFile(t, filepath.Join(app.c.cwd, "docs/report.md"), "# 测试结果\n\n35 项检查全部通过。\n")
	tt.Frame()
	clickReport := func() {
		t.Helper()
		r, ok := tt.Find("查看测试报告")
		if !ok {
			t.Fatal("report link missing")
		}
		// Find returns the full paragraph; the link occupies its first words.
		tt.ClickAt(r.X+12, r.Y+r.H/2)
	}
	clickReport()
	p := app.panel()
	ed := p.editors["docs/report.md"]
	if !p.visible || !app.panelVisible || p.active < 0 || p.surfaces[p.active].kind != surfaceFiles || p.selected != "docs/report.md" {
		t.Fatalf("report did not open in the files panel: visible=%v selected=%q texts=%q", p.visible, p.selected, tt.Texts())
	}
	if ed == nil || ed.state == nil || !ed.preview || !tt.HasText("测试结果") || !tt.HasText("35 项检查全部通过。") {
		t.Fatalf("report preview missing: %q", tt.Texts())
	}
	// Reopening a link reuses the files tab and the editor's unsaved content.
	ed.state.Buffer.Set("# 修改后的报告\n")
	app.hidePanel(p)
	tt.Frame()
	clickReport()
	if len(p.surfaces) != 1 || !p.visible || ed != p.editors["docs/report.md"] || !tt.HasText("修改后的报告") {
		t.Fatal("reopening the report lost the editor or duplicated the files tab")
	}
}

func TestMessageThreadLinkOpensConversation(t *testing.T) {
	app, tt := testApp(t)
	app.md.OnLink = app.openMessageLink
	target := Thread{ID: 2, ServerID: "target", Title: "Target conversation", HistoryLoaded: true}
	app.c.setThreads([]Thread{{ID: 1, ServerID: "source", HistoryLoaded: true, Messages: []Message{{ID: 1, Role: "assistant", Content: "[Open linked thread](" + app.c.threadRef(target).URL() + ")"}}}, target})
	app.c.setActive(1)
	app.collaborations.pane(app.c.threadRef(target).URL()).open = true
	tt.Frame()
	r, ok := tt.Find("Open linked thread")
	if !ok {
		t.Fatal("missing conversation link")
	}
	tt.ClickAt(r.X+12, r.Y+r.H/2)
	tt.Frame()
	if app.c.active() != target.ID || app.collaborations.pane(app.c.threadRef(target).URL()).open {
		t.Fatal("message link did not open target or retained task popover")
	}
}
