package appmodel

import (
	model "miya/internal/model"
	textutil "miya/internal/textutil"
	filepath "path/filepath"
	sort "sort"
	strings "strings"
)

type ThreadReference struct {
	Thread
	ProjectName string
}

// Match every project's conversation metadata without loading its messages.
func MatchingThreadRefs(projects []PhoneProject, self int, query string) []ThreadReference {
	q := strings.ToLower(query)
	seen := map[string]bool{}
	var out []ThreadReference
	for _, p := range projects {
		for _, t := range p.Threads {
			key := model.ThreadAgent(t) + ":" + t.ServerID
			if t.ID == 0 || t.ID == self || t.ServerID == "" || seen[key] {
				continue
			}
			seen[key] = true
			if q != "" && !strings.Contains(strings.ToLower(ThreadRefTitle(t)), q) && !strings.Contains(strings.ToLower(t.ServerID), q) {
				continue
			}
			out = append(out, ThreadReference{Thread: t, ProjectName: textutil.FirstNonEmpty(p.Name, filepath.Base(p.Path))})
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
