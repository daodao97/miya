package registry

import (
	"path/filepath"

	projectmodel "miya/internal/project/model"
)

func Remember(projects []projectmodel.Project, path string) ([]projectmodel.Project, bool) {
	for _, p := range projects {
		if p.Path == path {
			return projects, false
		}
	}
	items := append([]projectmodel.Project(nil), projects...)
	return append([]projectmodel.Project{{Name: filepath.Base(path), Path: path}}, items...), true
}

func Rename(projects []projectmodel.Project, path, name string) ([]projectmodel.Project, bool) {
	items := append([]projectmodel.Project(nil), projects...)
	for i := range items {
		if items[i].Path == path {
			items[i].Name = name
			return items, true
		}
	}
	return projects, false
}

func Remove(projects []projectmodel.Project, path string) []projectmodel.Project {
	items := append([]projectmodel.Project(nil), projects...)
	out := items[:0]
	for _, p := range items {
		if p.Path != path {
			out = append(out, p)
		}
	}
	return out
}
