package main

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
}

// AutomationRun is one run of an automation.
type AutomationRun struct {
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
	Report *RunReport `json:"report,omitempty"`
}

// A run's result is one of these.
const (
	runRunning     = "running"
	runPassed      = "passed"
	runFailed      = "failed"
	runInterrupted = "interrupted"
	runSkipped     = "skipped"
)

const (
	maxAutomationRounds  = 5
	automationRunHistory = 20
)

func runResultLabel(result string) string {
	switch result {
	case runRunning:
		return "Running"
	case runPassed:
		return "Passed"
	case runFailed:
		return "Failed"
	case runInterrupted:
		return "Interrupted"
	case runSkipped:
		return "Skipped"
	}
	return result
}

// normalize checks an automation and fills its defaults.
func (a *Automation) normalize() error {
	a.Name = strings.TrimSpace(a.Name)
	a.Project = strings.TrimSpace(a.Project)
	a.Instructions = strings.TrimSpace(a.Instructions)
	a.Schedule = strings.Join(strings.Fields(a.Schedule), " ")
	a.Check = strings.TrimSpace(a.Check)
	if a.Agent = normalizeAgentID(firstNonEmpty(a.Agent, agentCodex)); a.Agent == "" {
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
	if _, err := parseCron(a.Schedule); err != nil {
		return fmt.Errorf("Invalid schedule %q: %v", a.Schedule, err)
	}
	if a.Check == "" {
		a.Rounds = 1
	} else {
		a.Rounds = min(max(a.Rounds, 1), maxAutomationRounds)
	}
	if a.ID == "" {
		a.ID = newAutomationID()
	}
	return nil
}

func newAutomationID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Next is the automation's next scheduled time after after.
func (a Automation) Next(after time.Time) time.Time {
	s, err := parseCron(a.Schedule)
	if err != nil {
		return time.Time{}
	}
	return s.Next(after)
}

// automationStore is everything automations keep: what people set up and
// what their runs did.
type automationStore struct {
	Automations []Automation               `json:"automations"`
	Runs        map[string][]AutomationRun `json:"runs,omitempty"`
}

func automationsPath() string { return filepath.Join(serviceDir(), "automations.json") }

// loadAutomations reads the store; a missing or damaged file is an empty
// store, with damaged entries left out rather than stopping the others.
func loadAutomations(path string) automationStore {
	var s automationStore
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	valid := s.Automations[:0]
	for _, a := range s.Automations {
		if a.normalize() == nil {
			valid = append(valid, a)
		}
	}
	s.Automations = valid
	if s.Runs == nil {
		s.Runs = map[string][]AutomationRun{}
	}
	return s
}

func (s automationStore) save(path string) error {
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

func (s *automationStore) find(id string) (int, bool) {
	for i, a := range s.Automations {
		if a.ID == id {
			return i, true
		}
	}
	return -1, false
}

// lastRun is an automation's latest run, if it has one.
func (s automationStore) lastRun(id string) (AutomationRun, bool) {
	runs := s.Runs[id]
	if len(runs) == 0 {
		return AutomationRun{}, false
	}
	return runs[len(runs)-1], true
}

// addRun records a run, keeping the latest automationRunHistory.
func (s *automationStore) addRun(id string, r AutomationRun) {
	if s.Runs == nil {
		s.Runs = map[string][]AutomationRun{}
	}
	runs := append(s.Runs[id], r)
	if len(runs) > automationRunHistory {
		runs = runs[len(runs)-automationRunHistory:]
	}
	s.Runs[id] = runs
}

// updateLastRun changes an automation's latest run.
func (s *automationStore) updateLastRun(id string, change func(*AutomationRun)) {
	if runs := s.Runs[id]; len(runs) > 0 {
		change(&runs[len(runs)-1])
	}
}

// anyEnabled reports whether some automation is on, which keeps the
// service up and starts it at login.
func (s automationStore) anyEnabled() bool {
	for _, a := range s.Automations {
		if a.Enabled {
			return true
		}
	}
	return false
}
