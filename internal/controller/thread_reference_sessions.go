package controller

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	os "os"
	filepath "path/filepath"
	sort "sort"
)

// Fork metadata locates the original backend session after its sidebar row has
// moved to a new session. It is not an alias to the replacement session.
type referenceSession struct {
	Ref     appmodel.ThreadRef `json:"ref"`
	Title   string             `json:"title"`
	Project string             `json:"project"`
}

func (c *Controller) LoadForkedReferences() {
	data, err := os.ReadFile(filepath.Join(c.ReferenceDir, "reference-sessions.json"))
	if err != nil {
		return
	}
	var sessions []referenceSession
	if json.Unmarshal(data, &sessions) != nil {
		return
	}
	c.ForkedReferences = map[appmodel.ThreadRef]referenceSession{}
	for _, session := range sessions {
		if session.Ref.Valid() && session.Ref.Source == c.ReferenceSource {
			c.ForkedReferences[session.Ref] = session
		}
	}
}

func (c *Controller) rememberForkedReference(t appmodel.Thread, project string) {
	ref := c.ThreadRef(t)
	if !ref.Valid() {
		return
	}
	if c.ForkedReferences == nil {
		c.ForkedReferences = map[appmodel.ThreadRef]referenceSession{}
	}
	c.ForkedReferences[ref] = referenceSession{Ref: ref, Title: appmodel.ThreadRefTitle(t), Project: project}
	if t.HistoryLoaded {
		if c.threadHistory == nil {
			c.threadHistory = map[string][]appmodel.Message{}
		}
		c.threadHistory[ref.URL()] = append([]appmodel.Message(nil), t.Messages...)
	}
	if c.ReferenceDir == "" {
		return
	}
	sessions := make([]referenceSession, 0, len(c.ForkedReferences))
	for _, session := range c.ForkedReferences {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Ref.URL() < sessions[j].Ref.URL() })
	data, err := json.Marshal(sessions)
	if err == nil {
		err = appmodel.AtomicWriteFile(filepath.Join(c.ReferenceDir, "reference-sessions.json"), data)
	}
	if err != nil && c.SetStatus != nil {
		c.SetStatus("Unable to save original conversation reference: " + err.Error())
	}
}
