package delegation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

const storeVersion = 1

var taskIDPattern = regexp.MustCompile(`^task-[0-9a-f]{32}$`)
var deliveryIDPattern = regexp.MustCompile(`^delivery-[0-9a-f]{32}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type taskPointer struct {
	Revision uint64 `json:"revision"`
	Digest   string `json:"digest"`
	Summary  *Task  `json:"summary,omitempty"`
}
type diskIndex struct {
	Version    int                    `json:"schema_version"`
	Revision   uint64                 `json:"revision"`
	Tasks      map[string]taskPointer `json:"tasks"`
	Parents    map[string]string      `json:"parents"`
	Watches    map[string]Watch       `json:"watches"`
	Deliveries map[string]Delivery    `json:"deliveries"`
	Paused     map[string]bool        `json:"paused"`
	Operations map[string]Receipt     `json:"operations,omitempty"`
}

// Store is used only by the manager's serial worker, never on the UI executor.
// beforeIndex is a crash-injection seam; payloads are immutable once written.
type Store struct {
	dir            string
	beforeIndex    func() error
	committed      map[string]taskPointer
	committedTasks map[string]Task
}

func NewStore(dir string) *Store { return &Store{dir: dir} }
func taskPath(id string, revision uint64, hash string) string {
	return filepath.Join("tasks", id, fmt.Sprintf("%d-%s.json", revision, hash))
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func decodeStrict(data []byte, into any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("unexpected data after JSON object")
	}
	return nil
}

func (s *Store) Load() (Snapshot, error) {
	out := emptySnapshot()
	if s.dir == "" {
		return out, nil
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "index.json"))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var index diskIndex
	if err := decodeStrict(data, &index); err != nil {
		return out, fmt.Errorf("delegation index: %w", err)
	}
	if index.Version != storeVersion {
		return out, fmt.Errorf("unsupported delegation schema %d", index.Version)
	}
	if index.Tasks == nil || index.Parents == nil || index.Watches == nil || index.Deliveries == nil || index.Paused == nil {
		return out, fmt.Errorf("incomplete delegation index")
	}
	out.Revision, out.Parents, out.Watches, out.Deliveries, out.Paused = index.Revision, index.Parents, index.Watches, index.Deliveries, index.Paused
	if index.Operations != nil {
		out.Operations = index.Operations
	}
	for id, ptr := range index.Tasks {
		if !taskIDPattern.MatchString(id) || ptr.Revision == 0 {
			return out, fmt.Errorf("invalid delegation task pointer")
		}
		if ptr.Summary != nil {
			task := *ptr.Summary
			if ptr.Digest != "" || task.ID != id || task.Revision != ptr.Revision || !expiredSummary(task) {
				return out, fmt.Errorf("invalid expired task summary")
			}
			out.Tasks[id] = task
			continue
		}
		if !hashPattern.MatchString(ptr.Digest) {
			return out, fmt.Errorf("invalid delegation task digest")
		}
		data, err := os.ReadFile(filepath.Join(s.dir, taskPath(id, ptr.Revision, ptr.Digest)))
		if err != nil {
			return out, fmt.Errorf("delegation task %s: %w", id, err)
		}
		if digest(data) != ptr.Digest {
			return out, fmt.Errorf("delegation task %s checksum mismatch", id)
		}
		var task Task
		if err := decodeStrict(data, &task); err != nil {
			return out, fmt.Errorf("delegation task %s: %w", id, err)
		}
		if task.ID != id || task.Revision != ptr.Revision {
			return out, fmt.Errorf("delegation task %s identity mismatch", id)
		}
		out.Tasks[id] = task
	}
	s.committed = index.Tasks
	s.committedTasks = maps.Clone(out.Tasks)
	for id, t := range s.committedTasks {
		t.Result.Artifacts = slices.Clone(t.Result.Artifacts)
		s.committedTasks[id] = t
	}
	return out, nil
}

func (s *Store) Save(state Snapshot) error {
	if s.dir == "" {
		return nil
	}
	index := diskIndex{Version: storeVersion, Revision: state.Revision, Tasks: map[string]taskPointer{}, Parents: state.Parents, Watches: state.Watches, Deliveries: state.Deliveries, Paused: state.Paused, Operations: state.Operations}
	for id, task := range state.Tasks {
		if !taskIDPattern.MatchString(id) || task.ID != id || task.Revision == 0 {
			return fmt.Errorf("invalid task identity")
		}
		if pointer, ok := s.committed[id]; ok && pointer.Revision == task.Revision {
			if !reflect.DeepEqual(task, s.committedTasks[id]) {
				return fmt.Errorf("immutable task revision changed: %s", id)
			}
			index.Tasks[id] = pointer
			continue
		}
		if task.Result.Availability == ResultExpired {
			if !expiredSummary(task) {
				return fmt.Errorf("invalid expired task summary")
			}
			summary := task
			index.Tasks[id] = taskPointer{Revision: task.Revision, Summary: &summary}
			continue
		}
		data, err := json.Marshal(task)
		if err != nil {
			return err
		}
		file := filepath.Join(s.dir, taskPath(id, task.Revision, digest(data)))
		if old, err := os.ReadFile(file); err == nil {
			if !bytes.Equal(old, data) {
				return fmt.Errorf("immutable task revision changed: %s", id)
			}
		} else if !os.IsNotExist(err) {
			return err
		} else if err := atomicWrite(file, data); err != nil {
			return err
		}
		index.Tasks[id] = taskPointer{Revision: task.Revision, Digest: digest(data)}
	}
	// Persist the task directory entries before publishing pointers in the index.
	if entries, err := os.Stat(filepath.Join(s.dir, "tasks")); err == nil && entries.IsDir() {
		if err := syncDirectory(filepath.Join(s.dir, "tasks")); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if s.beforeIndex != nil {
		if err := s.beforeIndex(); err != nil {
			return err
		}
	}
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.dir, "index.json"), data); err != nil {
		return err
	}
	s.committed = index.Tasks
	s.committedTasks = maps.Clone(state.Tasks)
	for id, t := range s.committedTasks {
		t.Result.Artifacts = slices.Clone(t.Result.Artifacts)
		s.committedTasks[id] = t
	}
	return nil
}

func expiredSummary(t Task) bool {
	return Terminal(t.State) && t.Result.Availability == ResultExpired && t.Prompt == "" && t.Snapshot == "" && t.Result.Text == "" && t.Result.Error == "" && len(t.Result.Artifacts) == 0
}

// Prune only removes files absent from the successfully committed index. All
// reads hold task values in memory; no published reader depends on these files.
func (s *Store) Prune() error {
	if s.dir == "" {
		return nil
	}
	root := filepath.Join(s.dir, "tasks")
	dirs, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, dir := range dirs {
		if !dir.IsDir() || !taskIDPattern.MatchString(dir.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, dir.Name()))
		if err != nil {
			return err
		}
		ptr, committed := s.committed[dir.Name()]
		keep := ""
		if committed && ptr.Digest != "" {
			keep = filepath.Base(taskPath(dir.Name(), ptr.Revision, ptr.Digest))
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Name() == keep || !payloadNamePattern.MatchString(entry.Name()) {
				continue
			}
			if err := os.Remove(filepath.Join(root, dir.Name(), entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	// Native terminal evidence follows the same retention boundary as task
	// results. Remove it only after expiration/removal has committed to index.
	outcomes := filepath.Join(s.dir, "outcomes")
	entries, err := os.ReadDir(outcomes)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || entry.Name() != id+".json" || !taskIDPattern.MatchString(id) {
			continue
		}
		if task, ok := s.committedTasks[id]; ok && task.Result.Availability != ResultExpired {
			continue
		}
		if err := os.Remove(filepath.Join(outcomes, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

var payloadNamePattern = regexp.MustCompile(`^[0-9]+-[0-9a-f]{64}\.json$`)

func atomicWrite(file string, data []byte) (err error) {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".delegation-*")
	if err != nil {
		return err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), file); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(dir))
}
