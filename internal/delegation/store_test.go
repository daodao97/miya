package delegation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCrashBeforeIndexPreservesPreviousRevisionAndAllowsDifferentRetry(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("crash"))
	original, err := m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	m.store.beforeIndex = func() error { return errors.New("simulated crash after payload sync") }
	if err := m.Observe(Event{TaskID: task.ID, State: Completed, Result: Result{Text: "uncommitted result"}}); err == nil {
		t.Fatal("crash was not injected")
	}
	loaded, err := m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != original.Revision || loaded.Tasks[task.ID].State != Starting || loaded.Tasks[task.ID].Result.Text != "" {
		t.Fatal("uncommitted payload leaked through old index")
	}
	// The same numeric revision may have an orphaned payload from the failed save.
	// Its content hash makes a later, different revision payload safe to commit.
	m.store.beforeIndex = nil
	reopened, err := Open(m.store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Observe(Event{TaskID: task.ID, State: Failed, Result: Result{Error: "verified native rejection"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err = m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tasks[task.ID].State != Failed {
		t.Fatal("different recovery payload could not commit")
	}
}

func TestCorruptOrUnsupportedStoreDoesNotBecomeEmpty(t *testing.T) {
	for _, content := range []string{`{`, `{"schema_version":999}`, `{"schema_version":1,"revision":1}`} {
		t.Run(content, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "index.json")
			if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(NewStore(dir), Options{}); err == nil {
				t.Fatal("corrupt store accepted")
			}
			data, _ := os.ReadFile(file)
			if string(data) != content {
				t.Fatal("corrupt store overwritten")
			}
		})
	}
}

func TestStoreVerifiesChecksumAndRejectsPathTraversal(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("checksum"))
	state := m.View()
	data, err := os.ReadFile(filepath.Join(m.store.dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index diskIndex
	if err := decodeStrict(data, &index); err != nil {
		t.Fatal(err)
	}
	ptr := index.Tasks[task.ID]
	file := filepath.Join(m.store.dir, taskPath(task.ID, ptr.Revision, ptr.Digest))
	if err := os.WriteFile(file, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.Load(); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	delete(state.Tasks, task.ID)
	task.ID = "../../outside"
	state.Tasks[task.ID] = task
	if err := m.store.Save(state); err == nil {
		t.Fatal("path traversal accepted")
	}
}
