package controller

import (
	"sort"

	"miya/internal/phoneapi"
)

func (c *Controller) ThreadRefProjects() []phoneapi.Project {
	names := map[string]string{}
	for _, p := range c.Projects() {
		names[p.Path] = p.Name
	}
	// The live list wins over the current project's possibly stale cache.
	projects := []phoneapi.Project{{Path: c.Cwd, Name: names[c.Cwd], Threads: c.Threads()}}
	paths := make([]string, 0, len(c.ProjectCache))
	for path := range c.ProjectCache {
		if path != c.Cwd {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		projects = append(projects, phoneapi.Project{Path: path, Name: names[path], Threads: c.ProjectCache[path]})
	}
	return projects
}
