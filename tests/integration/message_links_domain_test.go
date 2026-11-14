//go:build !mygo_ui

package integration

import (
	msgfmt "miya/internal/chat/presentation"
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
