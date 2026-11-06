package controller

import (
	"miya/internal/model"
)

// AllThreads returns conversations across projects, with the open project first.

func (c *Controller) AllThreads() []model.Thread {
	out := append([]model.Thread(nil), c.Threads()...)
	for path, threads := range c.ProjectCache {
		if path != c.Cwd {
			out = append(out, threads...)
		}
	}
	return out
}

// projectThreads are the conversations of one project.

func (c *Controller) projectThreads(project string) []model.Thread {
	if project == "" || project == c.Cwd {
		return c.Threads()
	}
	return c.ProjectCache[project]
}
