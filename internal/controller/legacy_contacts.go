package controller

import (
	"path/filepath"
	"strings"

	"miya/internal/appdir"
)

// legacyContactWorkspace reports a daily-chat folder left by the removed
// contacts feature; it is internal data, never a project.
func legacyContactWorkspace(path string) bool {
	root := filepath.Join(appdir.Dir(), "contacts", "workspaces") + string(filepath.Separator)
	return strings.HasPrefix(path, root)
}
