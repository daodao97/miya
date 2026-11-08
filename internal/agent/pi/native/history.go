package pi

import (
	"encoding/json"
	"os"
	"path/filepath"

	"miya/internal/appdir"
	"miya/internal/chat/model"
)

// Record is Miya's copy of a pi conversation: the session file pi resumes
// from and the conversation as Miya shows it.
type Record struct {
	SessionFile string       `json:"sessionFile"`
	Thread      model.Thread `json:"thread"`
}

func HistoryPath(id string) string {
	if id == "" || filepath.Base(id) != id {
		return ""
	}
	dir := appdir.File("quickgui-pi")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id+".json")
}

func LoadRecord(id string) (Record, error) {
	var r Record
	data, err := os.ReadFile(HistoryPath(id))
	if err == nil {
		err = json.Unmarshal(data, &r)
	}
	return r, err
}

// SaveRecord replaces the record atomically. A conversation's session file
// survives saves that do not know it, and its messages survive saves of a
// conversation whose history is not loaded.
func SaveRecord(t model.Thread, sessionFile string) {
	p := HistoryPath(t.ServerID)
	if p == "" {
		return
	}
	if old, err := LoadRecord(t.ServerID); err == nil {
		if sessionFile == "" {
			sessionFile = old.SessionFile
		}
		// A conversation whose history was never loaded, such as one
		// suspended from the list, must not erase the saved messages.
		if !t.HistoryLoaded && len(t.Messages) == 0 {
			t.Messages = old.Thread.Messages
		}
	}
	data, err := json.Marshal(Record{SessionFile: sessionFile, Thread: t})
	if err != nil || os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".history-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		_ = os.Rename(name, p)
	}
}

func RemoveRecord(id string) {
	if p := HistoryPath(id); p != "" {
		_ = os.Remove(p)
	}
}
