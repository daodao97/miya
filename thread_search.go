package main

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type threadReference struct {
	Thread
	ProjectName string
}

// Match every project's conversation metadata without loading its messages.
func matchingThreadRefs(projects []phoneProject, self int, query string) []threadReference {
	q := strings.ToLower(strings.TrimPrefix(query, "#"))
	seen := map[int]bool{}
	var out []threadReference
	for _, p := range projects {
		for _, t := range p.Threads {
			if t.ID == 0 || t.ID == self || seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			if q != "" && !strings.Contains(strings.ToLower(threadRefTitle(t)), q) && !strings.HasPrefix(strconv.Itoa(t.ID), q) {
				continue
			}
			out = append(out, threadReference{Thread: t, ProjectName: firstNonEmpty(p.Name, filepath.Base(p.Path))})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastActivityAt != out[j].LastActivityAt {
			return out[i].LastActivityAt > out[j].LastActivityAt
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func (c *chatController) threadRefProjects() []phoneProject {
	names := map[string]string{}
	for _, p := range c.projects() {
		names[p.Path] = p.Name
	}
	// The live list wins over the current project's possibly stale cache.
	projects := []phoneProject{{Path: c.cwd, Name: names[c.cwd], Threads: c.threads()}}
	paths := make([]string, 0, len(c.projectCache))
	for path := range c.projectCache {
		if path != c.cwd {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		projects = append(projects, phoneProject{Path: path, Name: names[path], Threads: c.projectCache[path]})
	}
	return projects
}
