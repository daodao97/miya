package desktop

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Application IDs are bundle paths offered by the host, never commands or URLs.
type Application struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BundleID string `json:"bundleId,omitempty"`
	Icon     []byte `json:"icon,omitempty"`
}

func applicationDirs() []string {
	dirs := []string{"/Applications", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	return dirs
}

const finderApplication = "/System/Library/CoreServices/Finder.app"

// Scan standard app folders and one vendor/Utilities subfolder. Never descend
// into bundles: helper apps inside them are not launcher entries.
func applicationPaths(dirs []string, finder string) []Application {
	apps := []Application{}
	seen := map[string]bool{}
	add := func(path string) {
		if !strings.HasSuffix(path, ".app") || seen[path] {
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return
		}
		seen[path] = true
		apps = append(apps, Application{ID: path, Name: strings.TrimSuffix(filepath.Base(path), ".app")})
	}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if strings.HasSuffix(entry.Name(), ".app") {
				add(path)
			} else if entry.IsDir() {
				children, _ := os.ReadDir(path)
				for _, child := range children {
					if !strings.HasPrefix(child.Name(), ".") {
						add(filepath.Join(path, child.Name()))
					}
				}
			}
		}
	}
	if finder != "" {
		add(finder)
	}
	sort.Slice(apps, func(i, j int) bool {
		a, b := strings.ToLower(apps[i].Name), strings.ToLower(apps[j].Name)
		if a == b {
			return apps[i].ID < apps[j].ID
		}
		return a < b
	})
	return apps
}

func listedApplication(id string, apps []Application) bool {
	if !filepath.IsAbs(id) || filepath.Clean(id) != id {
		return false
	}
	for _, app := range apps {
		if app.ID == id {
			return true
		}
	}
	return false
}
