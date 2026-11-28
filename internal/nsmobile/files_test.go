package nsmobile

import (
	"strings"
	"testing"

	"miya/internal/remote"
)

func TestPhoneFileLinksUseDesktopCapabilities(t *testing.T) {
	app, m, _ := startPhone(t)
	changePhone(app, m, func() {
		m.Open, m.Path = 7, "/thread"
		m.Conn = new(remote.Conn)
		m.State.Files = true
		m.openMessageLink("/work/docs/report.md")
		if m.singleFile || m.filesURL.Get() == "" {
			t.Fatal("project file did not open through the project browser")
		}
		m.closeFileBrowser()
		m.openMessageLink("/tmp/remote-file.txt")
		if m.filesURL.Get() != "" || !strings.Contains(m.SendErr, "Update Miya on your desktop") {
			t.Fatalf("old desktop opened an unsupported file: %q", m.SendErr)
		}
		m.State.RemoteFiles = true
		m.openMessageLink("/tmp/remote-file.txt")
		if !m.singleFile || m.filesURL.Get() == "" {
			t.Fatal("outside-project file did not open in the single-file browser")
		}
	})
	app.Press("id:close-files")
	app.Settle()
	if m.filesURL.Get() != "" {
		t.Fatal("closing the file view left the proxy open")
	}
}
