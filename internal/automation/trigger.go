package automation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// An automation wakes when its Schedule comes round or one of its Triggers
// fires. Every way of waking makes a WakeEvent, which goes through the same
// pipeline (wake.go): it is matched, deduplicated, held back by the
// automation's WakePolicy, recorded, and, when it gets through, starts a run.
// docs/automations.md has the design.

// Trigger kinds. A Schedule is the automation's own field, not a trigger.
const (
	// TriggerCondition wakes when a probe of the computer turns true.
	TriggerCondition = "condition"
	// TriggerIngress wakes when something outside Miya asks: miya -wake, or
	// an HTTP request to the local wake endpoint.
	TriggerIngress = "ingress"
	// TriggerFile wakes when a file or a directory's entries change.
	TriggerFile = "file"
	// TriggerAfter wakes when another automation's run finishes.
	TriggerAfter = "after"
	// TriggerSystem wakes on a system event: the computer waking from sleep.
	TriggerSystem = "system"
)

// Wake sources that are not triggers.
const (
	SourceSchedule = "schedule"
	SourceManual   = "manual"
	// SourceAgent is a wake an agent asked for with wake_automation.
	SourceAgent = "agent"
)

// SystemWake is the system event of a computer waking from sleep.
const SystemWake = "wake"

const (
	defaultProbeEvery   = 5
	defaultProbeRepeat  = 24
	defaultMaxPerDay    = 24
	defaultFileDebounce = 30
)

// Trigger is one way an automation wakes besides its schedule.
type Trigger struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Condition is what a condition trigger probes.
	Condition *Condition `json:"condition,omitempty"`
	// Path is the file or directory a file trigger watches.
	Path string `json:"path,omitempty"`
	// After is the automation whose finished runs wake this one; Results,
	// when set, are the results that do.
	After   string   `json:"after,omitempty"`
	Results []string `json:"results,omitempty"`
	// Event is a system trigger's event: SystemWake.
	Event string `json:"event,omitempty"`
	// Token authorizes HTTP wakes of an ingress trigger.
	Token    string `json:"token,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// WakePolicy holds back wakes of an automation.
type WakePolicy struct {
	// Queue keeps one wake that comes while a run is in progress, to start
	// when it ends; without it such wakes are skipped.
	Queue bool `json:"queue,omitempty"`
	// DebounceSeconds gathers the wakes of a trigger that come within it
	// into one. File triggers gather for defaultFileDebounce without it.
	DebounceSeconds int `json:"debounceSeconds,omitempty"`
	// CooldownMinutes is the least time between runs started by triggers.
	CooldownMinutes int `json:"cooldownMinutes,omitempty"`
	// MaxPerDay is the most runs triggers start in 24 hours.
	MaxPerDay int `json:"maxPerDay,omitempty"`
	// Gate, when set, is probed on each wake but Run now: the run starts
	// only when it is met, as in "at 9:00, if free space is low".
	Gate *Condition `json:"gate,omitempty"`
}

// WakeEvent is something that happened and may start a run.
type WakeEvent struct {
	Automation string    `json:"automation"`
	Trigger    string    `json:"trigger,omitempty"`
	Source     string    `json:"source"`
	At         time.Time `json:"at,omitzero"`
	// Dedupe identifies an event delivered more than once: one with the
	// Dedupe of a recent wake is dropped.
	Dedupe string `json:"dedupe,omitempty"`
	// Reason says in a line why it woke.
	Reason string `json:"reason,omitempty"`
	// Data is what came with it from outside Miya: untrusted.
	Data    string `json:"data,omitempty"`
	CatchUp bool   `json:"catchUp,omitempty"`
	// Count is how many events a debounced one gathers.
	Count int `json:"count,omitempty"`
}

// Wake is what became of a WakeEvent.
type Wake struct {
	At      time.Time `json:"at"`
	Source  string    `json:"source"`
	Trigger string    `json:"trigger,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Dedupe  string    `json:"dedupe,omitempty"`
	Outcome string    `json:"outcome"`
	// Detail says why it did not start a run.
	Detail string `json:"detail,omitempty"`
	// Started is the run it started.
	Started time.Time `json:"started,omitzero"`
}

// A wake's outcome is one of these.
const (
	WakeStarted = "started"
	WakeQueued  = "queued"
	WakeSkipped = "skipped"
	WakeHeld    = "held"
	WakeFailed  = "failed"
	WakeDropped = "dropped"
)

// WakeHistory is how many wakes an automation keeps.
const WakeHistory = 50

// DataLimit bounds what an event brings from outside.
const DataLimit = 4000

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// expandHome turns a leading ~ into the home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// normalize checks a trigger of the automation self and fills its defaults.
func (t *Trigger) normalize(self string) error {
	t.Kind = strings.TrimSpace(t.Kind)
	switch t.Kind {
	case TriggerCondition:
		c := t.Condition
		if c == nil {
			return errors.New("A condition trigger needs a condition")
		}
		if err := c.normalize(); err != nil {
			return err
		}
	case TriggerIngress:
		if t.Token == "" {
			t.Token = newToken()
		}
	case TriggerFile:
		if t.Path = expandHome(strings.TrimSpace(t.Path)); t.Path == "" || !filepath.IsAbs(t.Path) {
			return errors.New("A file trigger needs an absolute path")
		}
		t.Path = filepath.Clean(t.Path)
	case TriggerAfter:
		if t.After = strings.TrimSpace(t.After); t.After == "" {
			return errors.New("An after trigger needs the automation it follows")
		}
		if t.After == self {
			return errors.New("An automation cannot follow itself")
		}
		var results []string
		for _, r := range t.Results {
			switch r = strings.TrimSpace(r); r {
			case Passed, Failed, Interrupted:
				results = append(results, r)
			default:
				return fmt.Errorf("Unknown run result %q", r)
			}
		}
		t.Results = results
	case TriggerSystem:
		if t.Event = strings.TrimSpace(t.Event); t.Event == "" {
			t.Event = SystemWake
		}
		if t.Event != SystemWake {
			return fmt.Errorf("Unknown system event %q", t.Event)
		}
	default:
		return fmt.Errorf("Unknown trigger %q", t.Kind)
	}
	if t.Kind != TriggerCondition {
		t.Condition = nil
	}
	if t.Kind != TriggerIngress {
		t.Token = ""
	}
	if t.ID == "" {
		t.ID = NewID()
	}
	return nil
}

// normalizeTriggers checks an automation's triggers and policy.
func (a *Automation) normalizeTriggers() error {
	seen := map[string]bool{}
	for i := range a.Triggers {
		t := &a.Triggers[i]
		if err := t.normalize(a.ID); err != nil {
			return err
		}
		for seen[t.ID] {
			t.ID = NewID()
		}
		seen[t.ID] = true
	}
	p := &a.Policy
	if p.Gate != nil {
		gate := Trigger{Kind: TriggerCondition, Condition: p.Gate, ID: "gate"}
		if err := gate.normalize(a.ID); err != nil {
			return fmt.Errorf("Gate: %v", err)
		}
		if p.Gate.When != WhenSuccess {
			return errors.New("Gate: a gate is met or not; it cannot wait for a change or a line")
		}
	}
	p.DebounceSeconds = min(max(p.DebounceSeconds, 0), 3600)
	p.CooldownMinutes = min(max(p.CooldownMinutes, 0), 7*24*60)
	if p.MaxPerDay <= 0 {
		p.MaxPerDay = defaultMaxPerDay
	}
	p.MaxPerDay = min(p.MaxPerDay, 1440)
	return nil
}

// Trigger finds one of the automation's triggers.
func (a Automation) Trigger(id string) (Trigger, bool) {
	for _, t := range a.Triggers {
		if t.ID == id {
			return t, true
		}
	}
	return Trigger{}, false
}

// HasTrigger reports whether a trigger of kind is on.
func (a Automation) HasTrigger(kind string) bool {
	for _, t := range a.Triggers {
		if t.Kind == kind && !t.Disabled {
			return true
		}
	}
	return false
}

// DescribeTrigger says when a trigger wakes; name gives the names of other
// automations.
func DescribeTrigger(t Trigger, name func(id string) string) string {
	switch t.Kind {
	case TriggerCondition:
		if t.Condition != nil {
			return describeCondition(*t.Condition)
		}
	case TriggerIngress:
		return tr("When woken from outside Miya")
	case TriggerFile:
		return fmt.Sprintf(tr("When %s changes"), t.Path)
	case TriggerAfter:
		other := t.After
		if name != nil {
			if n := name(t.After); n != "" {
				other = n
			}
		}
		return fmt.Sprintf(tr("After \"%s\" finishes"), other)
	case TriggerSystem:
		return tr("When the computer wakes from sleep")
	}
	return t.Kind
}

// WakeText says when an automation wakes: its schedule and its triggers.
func WakeText(a Automation, name func(id string) string) string {
	var parts []string
	if a.Schedule != "" {
		parts = append(parts, DescribeCron(a.Schedule))
	}
	for _, t := range a.Triggers {
		if !t.Disabled {
			parts = append(parts, DescribeTrigger(t, name))
		}
	}
	if len(parts) == 0 {
		return tr("Manual only")
	}
	return strings.Join(parts, " · ")
}

// SourceLabel names where a wake came from.
func SourceLabel(source string) string {
	switch source {
	case SourceSchedule:
		return tr("Schedule")
	case SourceManual:
		return tr("Run now")
	case SourceAgent:
		return tr("Conversation")
	case TriggerCondition:
		return tr("Condition")
	case TriggerIngress:
		return tr("External")
	case TriggerFile:
		return tr("File change")
	case TriggerAfter:
		return tr("After another automation")
	case TriggerSystem:
		return tr("System")
	}
	return source
}

// OutcomeLabel says what became of a wake.
func OutcomeLabel(outcome string) string {
	switch outcome {
	case WakeStarted:
		return tr("Started a run")
	case WakeQueued:
		return tr("Queued")
	case WakeSkipped:
		return tr("Skipped")
	case WakeHeld:
		return tr("Held back")
	case WakeFailed:
		return tr("Failed")
	case WakeDropped:
		return tr("Dropped")
	}
	return outcome
}
