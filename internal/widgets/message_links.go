package widgets

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"miya/internal/msgfmt"
)

// Copy file destinations rather than their displayed label or URL encoding.
// On phones cwd is the connected desktop's project, never a phone-local path.
func MessageLinkMenu(c *ui.Context, cwd string) func(*ui.Menu, string) {
	services := c.Services()
	return func(m *ui.Menu, link string) {
		label, text := tr("Copy link"), link
		if rel, ok := msgfmt.LocalFileLink(cwd, link); ok {
			label, text = tr("Copy path"), filepath.Clean(filepath.Join(cwd, rel))
		}
		if m.Item(label).Chosen() {
			services.WriteClipboard(text)
		}
		m.Separator()
		m.EditItems()
	}
}
