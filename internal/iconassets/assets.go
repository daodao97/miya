// Package iconassets holds Miya's Tabler icons as SVG documents, shared by
// the MyGo widgets and the NSGo widgets so neither names the other's
// framework. See icons/LICENSE.
package iconassets

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

//go:embed icons/*.svg icons/LICENSE
var files embed.FS

// SVG returns the document of the icon called name, as in "chevron-down".
func SVG(name string) ([]byte, error) { return files.ReadFile("icons/" + name + ".svg") }

// Names lists every icon, sorted.
func Names() []string {
	entries, _ := fs.ReadDir(files, "icons")
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".svg") {
			names = append(names, strings.TrimSuffix(path.Base(e.Name()), ".svg"))
		}
	}
	return names
}
