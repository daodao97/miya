// Package threadlist keeps the conversation list between launches: a cache
// of each project's conversations, so the sidebar shows them before the
// agents answer, and the projects to list.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"

	"miya/internal/appdir"
	"miya/internal/chat/model"
	"miya/internal/contacts"
)

type Cache struct {
	Projects map[string][]model.Thread `json:"projects"`
}

func CachePath() string { return appdir.File("quickgui-thread-cache.json") }

// threadMeta is what the sidebar's cache keeps of a conversation.
type Meta struct {
	Contact                                     *contacts.Session
	MemoryDisabled                              bool
	ID                                          int
	ServerID, Title, Agent, Project, Age        string
	LastActivityAt                              int64
	Resumed                                     bool
	LaunchModel, LaunchEffort, PermissionPolicy string
}

func MetaOf(t model.Thread) Meta {
	return Meta{Contact: t.Contact, MemoryDisabled: t.MemoryDisabled, ID: t.ID, ServerID: t.ServerID, Title: t.Title, Agent: t.Agent, Project: t.Project, Age: t.Age, LastActivityAt: t.LastActivityAt, Resumed: t.Resumed, LaunchModel: t.LaunchModel, LaunchEffort: t.LaunchEffort, PermissionPolicy: t.PermissionPolicy}
}

func Write(projects map[string][]model.Thread) {
	if projects == nil {
		return
	}
	path := CachePath()
	if path == "" {
		return
	}
	// The sidebar only needs metadata. Never persist message bodies or tool output.
	metadata := make(map[string][]model.Thread, len(projects))
	for project, threads := range projects {
		for _, thread := range threads {
			metadata[project] = append(metadata[project], model.Thread{
				Contact:        thread.Contact,
				MemoryDisabled: thread.MemoryDisabled,
				ID:             thread.ID, ServerID: thread.ServerID, Title: thread.Title, Agent: thread.Agent,
				Resumed: model.ThreadAgent(thread) != model.AgentCodex && thread.Resumed,
				Project: thread.Project, Age: thread.Age, LastActivityAt: thread.LastActivityAt,
				LaunchModel: thread.LaunchModel, LaunchEffort: thread.LaunchEffort, PermissionPolicy: thread.PermissionPolicy,
			})
		}
	}
	data, err := json.Marshal(Cache{Projects: metadata})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

// persistThreadListCache writes the sidebar's cache; tests replace it.
var Persist = Write

func Load() map[string][]model.Thread {
	path := CachePath()
	if path == "" {
		return map[string][]model.Thread{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string][]model.Thread{}
	}
	var cache Cache
	if json.Unmarshal(data, &cache) != nil || cache.Projects == nil {
		return map[string][]model.Thread{}
	}
	// Older caches persisted Resumed, but a Codex session belongs to the
	// app-server process that loaded it, not to the stored conversation.
	for _, threads := range cache.Projects {
		for i := range threads {
			model.ResetConnectionSession(&threads[i])
		}
	}
	return cache.Projects
}
