package integration

import (
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	desktopui "miya/internal/ui"
	"miya/internal/widgets"
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
			got, ok := msgfmt.LocalFileLink(cwd, tc.link)
			if !ok || got != tc.want {
				t.Fatalf("localFileLink(%q) = %q, %v; want %q", tc.link, got, ok, tc.want)
			}
		})
	}
	for _, link := range []string{"", "#results", "https://example.com/report.md", "mailto:test@example.com", "javascript:alert(1)", "//example.com/report.md", "file://server/report.md", "file:report.md", "docs/%00report.md", "docs/%5creport.md", "docs/%zz.md"} {
		if rel, ok := msgfmt.LocalFileLink(cwd, link); ok {
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
			app.C.(*controller.Controller).SetThreads([]model.Thread{{ID: 1, HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "assistant", Content: "[report.md](" + tc.link + ")"}}}})
			app.C.(*controller.Controller).SetActive(1)
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
				want = filepath.Join(app.C.(*controller.Controller).Cwd, want)
			}
			if got := tt.Clipboard(); got != want {
				t.Fatalf("copied %q, want %q", got, want)
			}
		})
	}
}

func TestMobileMessageFileLinkCopiesDesktopPath(t *testing.T) {
	m := testMobileApp()
	th := m.State.Projects[1].Threads[0]
	th.HistoryLoaded = true
	th.Messages = []model.Message{{ID: 1, Role: "assistant", Content: "[report.md](docs/report.md:42)"}}
	m.State.Projects[1].Threads[0] = th
	m.Selected = "/a"
	m.OpenThread(th)
	tt := ui.NewTester(m.ViewRoot, 390, 750)
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

func TestMobileLocalFileLinkClickReachesDesktopPreview(t *testing.T) {
	for _, link := range []string{"docs/report.md:42", "/b/docs/report.md", "file:///b/docs/report.md", "../a/source.go"} {
		t.Run(link, func(t *testing.T) {
			m := testMobileApp()
			m.Connected = false
			th := m.State.Projects[1].Threads[0]
			th.HistoryLoaded = true
			th.Messages = []model.Message{{ID: 1, Role: "assistant", Content: "[Open source](" + link + ")"}}
			m.State.Projects[1].Threads[0] = th
			m.OpenThread(th)
			tt := ui.NewTester(m.ViewRoot, 390, 750)
			tt.Frame()
			r, ok := tt.Find("Open source")
			if !ok {
				t.Fatal("file link missing")
			}
			tt.ClickAt(r.X+12, r.Y+r.H/2)
			if m.SendErr != "Not connected to desktop" {
				t.Fatalf("file click was ignored instead of reaching desktop preview: %q", m.SendErr)
			}
		})
	}
}

func TestMessageFileLinkMenusAcrossRenderers(t *testing.T) {
	for _, kind := range []string{"stream", "reference-paragraph", "reference-inline"} {
		t.Run(kind, func(t *testing.T) {
			app, _ := testApp(t)
			source := handoffSource()
			app.C.(*controller.Controller).SetThreads([]model.Thread{source})
			message := model.Message{Role: "assistant", Content: "[report.md](docs/report.md)"}
			if kind != "stream" {
				message.Role = "user"
				separator := "\n\n"
				if kind == "reference-inline" {
					separator = " "
				}
				message.Content += separator + model.ReferenceText(testReference(source), source.Title)
			}
			tt := ui.NewTester(func(c *ui.Context) {
				app.Md.LinkMenu = widgets.MessageLinkMenu(c, app.C.(*controller.Controller).Cwd)
				app.MessageView(c, message, kind == "stream")
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
			if got, want := tt.Clipboard(), filepath.Join(app.C.(*controller.Controller).Cwd, "docs/report.md"); got != want {
				t.Fatalf("copied %q, want %q", got, want)
			}
		})
	}
}

func TestReportLinkOpensFilesPanel(t *testing.T) {
	syncPanel(t)
	app, tt := testApp(t)
	app.Md.OnLink = app.OpenMessageLink
	app.C.(*controller.Controller).SetThreads([]model.Thread{{ID: 1, Title: "Report", HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "assistant", Content: "[查看测试报告](docs/report.md)"}}}})
	app.C.(*controller.Controller).SetActive(1)
	writeFile(t, filepath.Join(app.C.(*controller.Controller).Cwd, "docs/report.md"), "# 测试结果\n\n35 项检查全部通过。\n")
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
	p := app.Panel()
	ed := p.Editors["docs/report.md"]
	if !p.Visible || !app.PanelVisible || p.Active < 0 || p.Surfaces[p.Active].Kind != desktopui.SurfaceFiles || p.Selected != "docs/report.md" {
		t.Fatalf("report did not open in the files panel: visible=%v selected=%q texts=%q", p.Visible, p.Selected, tt.Texts())
	}
	if ed == nil || ed.State == nil || !ed.Preview || !tt.HasText("测试结果") || !tt.HasText("35 项检查全部通过。") {
		t.Fatalf("report preview missing: %q", tt.Texts())
	}
	// Reopening a link reuses the files tab and the editor's unsaved content.
	ed.State.Buffer.Set("# 修改后的报告\n")
	app.HidePanel(p)
	tt.Frame()
	clickReport()
	if len(p.Surfaces) != 1 || !p.Visible || ed != p.Editors["docs/report.md"] || !tt.HasText("修改后的报告") {
		t.Fatal("reopening the report lost the editor or duplicated the files tab")
	}
}

func TestMessageThreadLinkOpensConversation(t *testing.T) {
	app, tt := testApp(t)
	app.Md.OnLink = app.OpenMessageLink
	target := model.Thread{ID: 2, ServerID: "target", Title: "Target conversation", HistoryLoaded: true}
	app.C.(*controller.Controller).SetThreads([]model.Thread{{ID: 1, ServerID: "source", HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "assistant", Content: "[Open linked thread](" + app.C.(*controller.Controller).ThreadRef(target).URL() + ")"}}}, target})
	app.C.(*controller.Controller).SetActive(1)
	app.Collaborations.Pane(app.C.(*controller.Controller).ThreadRef(target).URL()).Open = true
	tt.Frame()
	r, ok := tt.Find("Open linked thread")
	if !ok {
		t.Fatal("missing conversation link")
	}
	tt.ClickAt(r.X+12, r.Y+r.H/2)
	tt.Frame()
	if app.C.(*controller.Controller).Active() != target.ID || app.Collaborations.Pane(app.C.(*controller.Controller).ThreadRef(target).URL()).Open {
		t.Fatal("message link did not open target or retained task popover")
	}
}
