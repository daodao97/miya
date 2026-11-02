package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Fork metadata locates the original backend session after its sidebar row has
// moved to a new session. It is not an alias to the replacement session.
type referenceSession struct {
	Ref     ThreadRef `json:"ref"`
	Title   string    `json:"title"`
	Project string    `json:"project"`
}

func (c *chatController) loadForkedReferences() {
	data, err := os.ReadFile(filepath.Join(c.referenceDir, "reference-sessions.json"))
	if err != nil {
		return
	}
	var sessions []referenceSession
	if json.Unmarshal(data, &sessions) != nil {
		return
	}
	c.forkedReferences = map[ThreadRef]referenceSession{}
	for _, session := range sessions {
		if session.Ref.valid() && session.Ref.Source == c.referenceSource {
			c.forkedReferences[session.Ref] = session
		}
	}
}

func (c *chatController) rememberForkedReference(t Thread, project string) {
	ref := c.threadRef(t)
	if !ref.valid() {
		return
	}
	if c.forkedReferences == nil {
		c.forkedReferences = map[ThreadRef]referenceSession{}
	}
	c.forkedReferences[ref] = referenceSession{Ref: ref, Title: threadRefTitle(t), Project: project}
	if t.HistoryLoaded {
		if c.threadHistory == nil {
			c.threadHistory = map[string][]Message{}
		}
		c.threadHistory[ref.URL()] = append([]Message(nil), t.Messages...)
	}
	if c.referenceDir == "" {
		return
	}
	sessions := make([]referenceSession, 0, len(c.forkedReferences))
	for _, session := range c.forkedReferences {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Ref.URL() < sessions[j].Ref.URL() })
	data, err := json.Marshal(sessions)
	if err == nil {
		err = atomicWriteFile(filepath.Join(c.referenceDir, "reference-sessions.json"), data)
	}
	if err != nil && c.setStatus != nil {
		c.setStatus("无法保存原会话引用：" + err.Error())
	}
}

func atomicWriteFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".references-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
