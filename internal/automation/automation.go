// Package automation holds scheduled automations: their settings, cron
// schedules, run history and structured results, and drafting one from a
// request with an agent.
package automation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/appdir"
	"miya/internal/chat/model"
	"miya/internal/project/worktree"
	"miya/internal/textutil"
	"miya/locales"
)

// An automation starts a conversation in a project on a schedule, with the
// same instructions each time. With a check command, a round that does not
// pass it is followed by another in the same conversation, up to Rounds.
// The background service runs them (automation_run.go); docs/automations.md
// has the design.
type Automation struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Agent   string `json:"agent"`
	// Request is what the person wrote; the agent made the rest of it.
	Request      string `json:"request,omitempty"`
	Instructions string `json:"instructions"`
	Schedule     string `json:"schedule"`
	// Check runs in the project after each round; exit status 0 passes.
	Check   string    `json:"check,omitempty"`
	Rounds  int       `json:"rounds"`
	Enabled bool      `json:"enabled"`
	Created time.Time `json:"created"`
	// Due is the last scheduled time a run was started or skipped for.
	Due time.Time `json:"due,omitzero"`
	// NoCarryOver leaves the previous result out of the next run.
	NoCarryOver bool `json:"noCarryOver,omitempty"`
	// Worktree runs each run in a new Git worktree on its own branch, so
	// that it cannot collide with edits in the project (worktree.go).
	Worktree bool `json:"worktree,omitempty"`
}

// Run is one run of an automation.
type Run struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Thread   int       `json:"thread,omitempty"`
	ServerID string    `json:"serverId,omitempty"`
	Round    int       `json:"round,omitempty"`
	Rounds   int       `json:"rounds,omitempty"`
	Result   string    `json:"result"`
	// Summary is the first line of the agent's last reply, or why the run
	// ended.
	Summary string `json:"summary,omitempty"`
	// CatchUp marks a run started for a time missed while asleep.
	CatchUp bool `json:"catchUp,omitempty"`
	// Report is the run\'s structured result.
	Report *Report `json:"report,omitempty"`
	// Worktree is where a run of a Worktree automation worked, and
	// WorktreeState what became of it.
	Worktree      *worktree.Worktree `json:"worktree,omitempty"`
	WorktreeState string             `json:"worktreeState,omitempty"`
	Changes       worktree.Changes   `json:"changes,omitzero"`
	// WorktreeError is why the last merge or discard failed.
	WorktreeError string `json:"worktreeError,omitempty"`
}

// A run's result is one of these.
const (
	Running     = "running"
	Passed      = "passed"
	Failed      = "failed"
	Interrupted = "interrupted"
	Skipped     = "skipped"
)

const (
	maxAutomationRounds = 5
	RunHistory          = 20
)

func ResultLabel(result string) string {
	switch result {
	case Running:
		return "Running"
	case Passed:
		return "Passed"
	case Failed:
		return "Failed"
	case Interrupted:
		return "Interrupted"
	case Skipped:
		return "Skipped"
	}
	return result
}

// Normalize checks an automation and fills its defaults.
func (a *Automation) Normalize() error {
	a.Name = strings.TrimSpace(a.Name)
	a.Project = strings.TrimSpace(a.Project)
	a.Instructions = strings.TrimSpace(a.Instructions)
	a.Schedule = strings.Join(strings.Fields(a.Schedule), " ")
	a.Check = strings.TrimSpace(a.Check)
	if a.Agent = model.NormalizeAgentID(textutil.FirstNonEmpty(a.Agent, model.AgentCodex)); a.Agent == "" {
		return errors.New("Agent must be codex or claude-code")
	}
	switch {
	case a.Name == "":
		return errors.New("Name is required")
	case a.Project == "":
		return errors.New("Project is required")
	case a.Instructions == "":
		return errors.New("Instructions are required")
	}
	if _, err := ParseCron(a.Schedule); err != nil {
		return fmt.Errorf("Invalid schedule %q: %v", a.Schedule, err)
	}
	if a.Check == "" {
		a.Rounds = 1
	} else {
		a.Rounds = min(max(a.Rounds, 1), maxAutomationRounds)
	}
	if a.ID == "" {
		a.ID = NewID()
	}
	return nil
}

func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Next is the automation's next scheduled time after after.
func (a Automation) Next(after time.Time) time.Time {
	s, err := ParseCron(a.Schedule)
	if err != nil {
		return time.Time{}
	}
	return s.Next(after)
}

// Store is everything automations keep: what people set up and
// what their runs did.
type Store struct {
	Automations []Automation     `json:"automations"`
	Runs        map[string][]Run `json:"runs,omitempty"`
}

func Path() string { return filepath.Join(appdir.Dir(), "automations.json") }

// Load reads the store; a missing or damaged file is an empty
// store, with damaged entries left out rather than stopping the others.
func Load(path string) Store {
	var s Store
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	valid := s.Automations[:0]
	for _, a := range s.Automations {
		if a.Normalize() == nil {
			valid = append(valid, a)
		}
	}
	s.Automations = valid
	if s.Runs == nil {
		s.Runs = map[string][]Run{}
	}
	return s
}

func (s Store) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) Find(id string) (int, bool) {
	for i, a := range s.Automations {
		if a.ID == id {
			return i, true
		}
	}
	return -1, false
}

// LastRun is an automation's latest run, if it has one.
func (s Store) LastRun(id string) (Run, bool) {
	runs := s.Runs[id]
	if len(runs) == 0 {
		return Run{}, false
	}
	return runs[len(runs)-1], true
}

// AddRun records a run, keeping the latest RunHistory.
func (s *Store) AddRun(id string, r Run) {
	if s.Runs == nil {
		s.Runs = map[string][]Run{}
	}
	runs := append(s.Runs[id], r)
	if len(runs) > RunHistory {
		runs = runs[len(runs)-RunHistory:]
	}
	s.Runs[id] = runs
}

// UpdateRun changes the run of an automation that started at started,
// reporting whether there is one.
func (s *Store) UpdateRun(id string, started time.Time, change func(*Run)) bool {
	runs := s.Runs[id]
	for i := range runs {
		if runs[i].Started.Equal(started) {
			change(&runs[i])
			return true
		}
	}
	return false
}

// UpdateLastRun changes an automation's latest run.
func (s *Store) UpdateLastRun(id string, change func(*Run)) {
	if runs := s.Runs[id]; len(runs) > 0 {
		change(&runs[len(runs)-1])
	}
}

// AnyEnabled reports whether some automation is on, which keeps the
// service up and starts it at login.
func (s Store) AnyEnabled() bool {
	for _, a := range s.Automations {
		if a.Enabled {
			return true
		}
	}
	return false
}

func tr(s string) string { return locales.T(s) }
