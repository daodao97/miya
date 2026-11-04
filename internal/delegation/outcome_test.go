package delegation

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func claudeOutcomeTask(t *testing.T, m *Manager, notify bool) (Task, Event) {
	t.Helper()
	in := input("claude-outcome")
	in.Agent, in.Notify = "claude-code", notify
	task := create(t, m, in)
	target := strings.Replace(ref("child"), "/codex/", "/claude-code/", 1)
	bound := Event{TaskID: task.ID, Target: target, SessionEpoch: "claude:original", NativeResultID: "root-result", State: Running}
	observe(t, m, bound)
	bound.State, bound.Result = Completed, Result{Text: "fixed native result"}
	task, _ = m.Read(task.ID)
	return task, bound
}

func TestNativeOutcomeRemainsReadableWithLegacyEventChecksum(t *testing.T) {
	m, _ := harness(t, 4)
	task, event := claudeOutcomeTask(t, m, false)
	event.Result = boundedResult(event.Result)
	// The original on-disk Event did not have SubmissionPhase. Its field
	// order and zero fields are part of the saved canonical checksum.
	legacy := struct {
		TaskID, Target, NativeTurnID, NativeResultID, SessionEpoch string
		State                                                      State
		WaitCause, Progress                                        string
		Result                                                     Result
		Sequence, ExpectedRevision                                 uint64
	}{TaskID: event.TaskID, Target: event.Target, NativeResultID: event.NativeResultID, SessionEpoch: event.SessionEpoch, State: event.State, Result: event.Result}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(struct {
		Version int             `json:"version"`
		Digest  string          `json:"sha256"`
		Event   json.RawMessage `json:"event"`
	}{1, digest(payload), payload})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(m.store.dir, "outcomes", task.ID+".json")
	if err = atomicWrite(file, record); err != nil {
		t.Fatal(err)
	}
	if proof, err := m.NativeOutcome(task.ID); err != nil || proof.Result.Text != event.Result.Text {
		t.Fatal("upgrade invalidated existing terminal evidence", err)
	}
}

func TestNativeOutcomeRecheckAfterIndexFailureKeepsResultAndNotificationAtomic(t *testing.T) {
	m, _ := harness(t, 4)
	task, event := claudeOutcomeTask(t, m, true)
	if err := m.RecordNativeOutcome(event); err != nil {
		t.Fatal(err)
	}
	m.store.beforeIndex = func() error { return errors.New("crash before result index") }
	if err := m.Observe(event); !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal(err)
	}
	recovered, err := Open(NewStore(m.store.dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(); err != nil {
		t.Fatal(err)
	}
	before, _ := recovered.Read(task.ID)
	if before.State != NeedsAttention || before.Result.Text != "" || len(recovered.View().Deliveries) != 0 {
		t.Fatal("uncommitted outcome was automatically published", before)
	}
	proof, err := recovered.NativeOutcome(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	observe(t, recovered, proof)
	observe(t, recovered, proof)
	after, _ := recovered.Read(task.ID)
	if after.State != Completed || after.Result.Text != event.Result.Text || len(recovered.View().Deliveries) != 1 {
		t.Fatal("rechecked result and exactly one notification were not committed together", after)
	}
}

func TestNativeOutcomeRejectsOtherExecutionAndDamagedEvidence(t *testing.T) {
	for _, field := range []string{"target", "epoch", "turn", "state", "missing", "checksum"} {
		t.Run(field, func(t *testing.T) {
			m, _ := harness(t, 4)
			task, event := claudeOutcomeTask(t, m, false)
			switch field {
			case "target":
				event.Target += "other"
			case "epoch":
				event.SessionEpoch = "claude:later"
			case "turn":
				event.NativeTurnID = "other"
			case "state":
				event.State = Running
			}
			if field == "missing" {
				if _, err := m.NativeOutcome(task.ID); !IsCode(err, "RECHECK_UNAVAILABLE") {
					t.Fatal(err)
				}
			} else if field == "checksum" {
				if err := m.RecordNativeOutcome(event); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(m.store.dir, "outcomes", task.ID+".json")
				data, _ := os.ReadFile(file)
				data = bytes.ReplaceAll(data, []byte("fixed native result"), []byte("later native result"))
				if err := os.WriteFile(file, data, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := m.NativeOutcome(task.ID); !IsCode(err, "RECHECK_UNAVAILABLE") {
					t.Fatal(err)
				}
			} else if err := m.RecordNativeOutcome(event); !IsCode(err, "STALE_EVENT") {
				t.Fatal(err)
			}
			got, _ := m.Read(task.ID)
			if got.State != Running || got.Result.Text != "" {
				t.Fatal("unverifiable evidence changed task", got)
			}
		})
	}
}

func TestNativeOutcomeIsBoundedImmutableAndExpiresAfterIndexCommit(t *testing.T) {
	m, now := harness(t, 4)
	task, event := claudeOutcomeTask(t, m, false)
	event.Result.Text = strings.Repeat("中", ResultLimit)
	if err := m.RecordNativeOutcome(event); err != nil {
		t.Fatal(err)
	}
	proof, err := m.NativeOutcome(task.ID)
	if err != nil || len(proof.Result.Text) > ResultLimit || !utf8.ValidString(proof.Result.Text) || !proof.Result.Truncated {
		t.Fatal("native evidence bypassed result bounds", err)
	}
	event.Result.Text = "different result"
	if err := m.RecordNativeOutcome(event); !IsCode(err, "STALE_EVENT") {
		t.Fatal("immutable evidence replaced", err)
	}
	observe(t, m, proof)
	got, _ := m.Read(task.ID)
	if !got.Result.Truncated {
		t.Fatal("recheck lost the truncation marker")
	}
	*now = now.Add(31 * 24 * time.Hour)
	m.store.beforeIndex = func() error { return errors.New("expiration index failed") }
	if err := m.Collect(); err == nil {
		t.Fatal("expected expiration failure")
	}
	file := filepath.Join(m.store.dir, "outcomes", task.ID+".json")
	if _, err := os.Stat(file); err != nil {
		t.Fatal("uncommitted expiration deleted evidence", err)
	}
	recovered, err := Open(NewStore(m.store.dir), Options{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("expired evidence retained", err)
	}
	if _, err := recovered.NativeOutcome(task.ID); !IsCode(err, "RECHECK_UNAVAILABLE") {
		t.Fatal("expired evidence restored result", err)
	}
}

func TestNativeOutcomeWriteFailureKeepsUncommittedResultAndNotificationPrivate(t *testing.T) {
	m, _ := harness(t, 4)
	task, event := claudeOutcomeTask(t, m, true)
	file := filepath.Join(m.store.dir, "outcomes", task.ID+".json")
	if err := os.MkdirAll(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordNativeOutcome(event); !IsCode(err, "STORAGE_UNAVAILABLE") || m.StorageError() == "" {
		t.Fatal("native outcome write failure was not exposed", err)
	}
	if err := m.Observe(event); !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal("failed evidence write allowed task publication", err)
	}
	got, _ := m.Read(task.ID)
	if got.State != Running || got.Result.Text != "" || len(m.View().Deliveries) != 0 {
		t.Fatal("failed native outcome became a visible result/notification", got)
	}
}
