package delegation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Native outcomes are evidence from an adapter, not a committed task revision.
// Saving one before Observe allows an explicit recheck after a crash between
// receiving Claude's root result and committing the task/result/delivery index.
// Neither admission nor automatic delivery reads these files.
type nativeOutcome struct {
	Version int    `json:"version"`
	Digest  string `json:"sha256"`
	Event   Event  `json:"event"`
}

func outcomeMatches(t Task, e Event) bool {
	return t.Agent == "claude-code" && t.ID == e.TaskID && t.Target != "" && t.Target == e.Target &&
		t.SessionEpoch != "" && t.SessionEpoch == e.SessionEpoch && t.NativeTurnID == e.NativeTurnID &&
		e.NativeResultID != "" && len(e.NativeResultID) <= 128 && Terminal(e.State)
}

// RecordNativeOutcome must run on the collaboration worker. A later process or
// conversation cannot replace evidence for a previously bound execution.
func (m *Manager) RecordNativeOutcome(e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.state.Tasks[e.TaskID]
	if !ok || !outcomeMatches(t, e) || Terminal(t.State) {
		return errCode("STALE_EVENT", "Native outcome does not match the persisted execution")
	}
	// Apply the same bounds used by Observe; the evidence must not bypass the
	// result retention limits or keep unbounded model output on disk.
	e.Result = boundedResult(e.Result)
	e.Sequence, e.ExpectedRevision, e.Progress, e.WaitCause = 0, 0, "", ""
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data, err := json.Marshal(nativeOutcome{Version: 1, Digest: digest(payload), Event: e})
	if err == nil && m.store.dir != "" {
		file := filepath.Join(m.store.dir, "outcomes", t.ID+".json")
		old, readErr := os.ReadFile(file)
		if readErr == nil {
			if !bytes.Equal(old, data) {
				return errCode("STALE_EVENT", "Native terminal evidence is immutable")
			}
		} else if !os.IsNotExist(readErr) {
			err = readErr
		} else {
			err = atomicWrite(file, data)
		}
	}
	if err != nil {
		m.writeError = err
		message := err.Error()
		m.storageError.Store(&message)
		return errCode("STORAGE_UNAVAILABLE", err.Error())
	}
	return nil
}

func (m *Manager) NativeOutcome(id string) (Event, error) {
	t, err := m.Read(id)
	if err != nil {
		return Event{}, err
	}
	if m.store.dir == "" || !taskIDPattern.MatchString(id) || t.Result.Availability == ResultExpired {
		return Event{}, errCode("RECHECK_UNAVAILABLE", "No saved native outcome is available")
	}
	data, err := os.ReadFile(filepath.Join(m.store.dir, "outcomes", id+".json"))
	if err != nil {
		return Event{}, errCode("RECHECK_UNAVAILABLE", fmt.Sprintf("Cannot read native outcome: %v", err))
	}
	var record nativeOutcome
	if err := decodeStrict(data, &record); err != nil || record.Version != 1 || !outcomeMatches(t, record.Event) {
		return Event{}, errCode("RECHECK_UNAVAILABLE", "Saved native outcome is damaged or belongs to another execution")
	}
	payload, err := json.Marshal(record.Event)
	if err != nil || record.Digest != digest(payload) {
		return Event{}, errCode("RECHECK_UNAVAILABLE", "Saved native outcome checksum does not match")
	}
	return record.Event, nil
}
