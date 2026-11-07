package presentation

import "miya/internal/chat/model"

// SidebarActivityThreads groups activity across projects in their displayed
// order, independent of the selected project and compact project lists.
func SidebarActivityThreads(projects []model.ProjectSnapshot, unread func(model.Thread) bool) (running, completed []model.Thread) {
	seen := map[int]bool{}
	for _, project := range projects {
		for _, thread := range project.Threads {
			if seen[thread.ID] {
				continue
			}
			seen[thread.ID] = true
			if thread.Loading {
				continue
			}
			if thread.Busy {
				running = append(running, thread)
			} else if unread != nil && unread(thread) {
				completed = append(completed, thread)
			}
		}
	}
	return DisplayedThreads(running, true), DisplayedThreads(completed, true)
}
