package main

import (
	"net/url"
	"path/filepath"
	"testing"
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
