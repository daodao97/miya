//go:build !mygo_ui

package integration

import (
	"miya/internal/chat/model"
	projectmodel "miya/internal/project/model"
	svcproto "miya/internal/service/protocol"
)

func sidebarNavigationState(project string, sameTime bool) svcproto.State {
	first := model.Thread{ID: 1, Title: "First running conversation", Busy: true, LastActivityAt: 10}
	second := model.Thread{ID: 2, Title: "Second running conversation", Busy: true, LastActivityAt: 20}
	if sameTime {
		second.LastActivityAt = first.LastActivityAt
	}
	state := svcproto.State{
		Cwd: project, Locale: "en",
		Projects:       []projectmodel.Project{{Name: "first", Path: "/first"}, {Name: "second", Path: "/second"}},
		ProjectThreads: map[string][]model.Thread{},
	}
	if project == "/first" {
		state.Threads, state.ProjectThreads["/second"] = []model.Thread{first}, []model.Thread{second}
	} else {
		state.Threads, state.ProjectThreads["/first"] = []model.Thread{second}, []model.Thread{first}
	}
	return state
}
