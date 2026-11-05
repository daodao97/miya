package main

import (
	"encoding/json"
	"miya/internal/appdir"
	"os"
	"path/filepath"
)

func projectOrderPath() string { return appdir.File("quickgui-project-order.json") }
func persistProjectOrder(projects []Project) {
	path := projectOrderPath()
	if path == "" {
		return
	}
	paths := make([]string, 0, len(projects))
	for _, p := range projects {
		paths = append(paths, p.Path)
	}
	data, err := json.Marshal(paths)
	if err != nil {
		return
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	// Replace atomically so an interrupted write cannot lose the saved order.
	f, err := os.CreateTemp(filepath.Dir(path), ".quickgui-project-order-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(f.Name(), path)
	}
}
func restoreProjectOrder(projects []Project) []Project {
	var paths []string
	data, err := os.ReadFile(projectOrderPath())
	if err != nil || json.Unmarshal(data, &paths) != nil {
		return projects
	}
	byPath := make(map[string]Project, len(projects))
	for _, p := range projects {
		byPath[p.Path] = p
	}
	out := make([]Project, 0, len(projects))
	for _, path := range paths {
		if p, ok := byPath[path]; ok {
			out = append(out, p)
			delete(byPath, path)
		}
	}
	for _, p := range projects {
		if _, ok := byPath[p.Path]; ok {
			out = append(out, p)
			delete(byPath, p.Path)
		}
	}
	return out
}
