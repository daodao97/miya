package registry

import (
	"path/filepath"

	projectmodel "miya/internal/project/model"
)

func LoadProjects(current string, trusted []string) []projectmodel.Project {
	seen := map[string]bool{}
	projects := []projectmodel.Project{}
	add := func(path string) {
		path, _ = filepath.Abs(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		projects = append(projects, projectmodel.Project{Name: filepath.Base(path), Path: path})
	}
	for _, path := range trusted {
		add(path)
	}
	add(current)
	return projects
}
