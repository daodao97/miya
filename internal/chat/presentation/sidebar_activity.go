package presentation

import "miya/internal/chat/model"

// SidebarActivityThreads groups activity across projects in their displayed
// order, independent of the selected project and compact project lists. A
// conversation that waits for the person is listed as waiting only, whether
// its turn runs or the person read it.
func SidebarActivityThreads(projects []model.ProjectSnapshot, waits, unread func(model.Thread) bool) (waiting, running, completed []model.Thread) {
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
			if waits != nil && waits(thread) {
				waiting = append(waiting, thread)
			} else if thread.Busy {
				running = append(running, thread)
			} else if unread != nil && unread(thread) {
				completed = append(completed, thread)
			}
		}
	}
	return DisplayedThreads(waiting, true), DisplayedThreads(running, true), DisplayedThreads(completed, true)
}
