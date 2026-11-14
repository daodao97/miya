package automation

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/chat/model"
)

// View is an automation as the page shows it.
type View struct {
	Automation
	ScheduleText string      `json:"scheduleText"`
	Next         time.Time   `json:"next,omitzero"`
	Upcoming     []time.Time `json:"upcoming,omitempty"`
	Running      bool        `json:"running,omitempty"`
	Runs         []Run       `json:"runs,omitempty"`
	// TimeMissing marks a draft whose sentence named no time.
	TimeMissing bool `json:"timeMissing,omitempty"`
	// Trials are what the test run of a draft's conditions found, by
	// trigger ID, and GateTrial for its gate.
	Trials map[string]Trial `json:"trials,omitempty"`
	// TriggerTexts say when each trigger wakes it, by trigger ID, and
	// TriggerStates what each has seen.
	TriggerTexts  map[string]string       `json:"triggerTexts,omitempty"`
	TriggerStates map[string]TriggerState `json:"triggerStates,omitempty"`
	// Wakes are its latest wakes, newest last.
	Wakes []Wake `json:"wakes,omitempty"`
	// WakeCommand wakes it from a terminal when it has an ingress trigger.
	WakeCommand string `json:"wakeCommand,omitempty"`
	// WakeURLs are where its ingress triggers are woken over HTTP, by
	// trigger ID, while the local wake endpoint listens.
	WakeURLs map[string]string `json:"wakeURLs,omitempty"`
}

type Request struct {
	ID         string      `json:"id,omitempty"`
	Automation *Automation `json:"automation,omitempty"`
	Enabled    bool        `json:"enabled,omitempty"`
	// Request, Project and Agent ask for a draft.
	Request string `json:"request,omitempty"`
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
	// Started and Action pick a run and merge or discard its worktree.
	Started time.Time `json:"started,omitzero"`
	Action  string    `json:"action,omitempty"`
	// Trigger, Data and Dedupe wake an automation from outside Miya;
	// ID may then be its name.
	Trigger string `json:"trigger,omitempty"`
	Data    string `json:"data,omitempty"`
	Dedupe  string `json:"dedupe,omitempty"`
}

func ViewOf(a Automation, runs []Run, running bool, now time.Time) View {
	v := View{Automation: a, ScheduleText: WakeText(a, nil), Running: running, Runs: runs}
	for _, t := range a.Triggers {
		if v.TriggerTexts == nil {
			v.TriggerTexts = map[string]string{}
		}
		v.TriggerTexts[t.ID] = DescribeTrigger(t, nil)
	}
	next := now
	for i := 0; i < 3; i++ {
		if next = a.Next(next); next.IsZero() {
			break
		}
		v.Upcoming = append(v.Upcoming, next)
	}
	if len(v.Upcoming) > 0 && a.Enabled {
		v.Next = v.Upcoming[0]
	}
	return v
}

func CheckText(a Automation) string {
	if a.Check == "" {
		return "No check; 1 round"
	}
	return fmt.Sprintf("Check: %s; up to %d rounds", a.Check, a.Rounds)
}

// WhereText says where an automation's runs work.
func WhereText(a Automation) string {
	if a.Worktree {
		return "a separate Git worktree on its own branch"
	}
	return "the project directory"
}

// CardDetail is what the confirmation card shows of a new one.
func (rn *Runner) CardDetail(a Automation, now time.Time) string {
	when := "Wakes: " + WakeText(a, rn.name)
	if a.Schedule != "" {
		when += fmt.Sprintf(" (%s); next scheduled run: %s", a.Schedule, FormatTime(a.Next(now)))
	}
	lines := []string{when}
	if a.HasTrigger(TriggerIngress) || a.HasTrigger(TriggerFile) {
		lines = append(lines, "Can be woken from outside Miya; what comes with a wake reaches the agent as data")
	}
	if a.Policy.Gate != nil {
		lines = append(lines, "Runs only if met: "+DescribeTrigger(Trigger{Kind: TriggerCondition, Condition: a.Policy.Gate}, nil))
	}
	return strings.Join(append(lines,
		"Project: "+a.Project,
		"Agent: "+model.AgentName(a.Agent),
		"Completion check: "+CheckText(a),
		"Runs in: "+WhereText(a),
		"Instructions: "+a.Instructions,
	), "\n")
}

// Diff lists what a change changes, besides turning it on or off.
func (rn *Runner) Diff(old, next Automation) string {
	var lines []string
	add := func(label, from, to string) {
		if from != to {
			lines = append(lines, label+": "+from+" → "+to)
		}
	}
	add("Name", old.Name, next.Name)
	add("Schedule", textOr(DescribeCron(old.Schedule), "none"), textOr(DescribeCron(next.Schedule), "none"))
	add("Triggers", rn.triggersText(old), rn.triggersText(next))
	add("Wake policy", PolicyText(old.Policy), PolicyText(next.Policy))
	add("Agent", model.AgentName(old.Agent), model.AgentName(next.Agent))
	add("Completion check", CheckText(old), CheckText(next))
	add("Runs in", WhereText(old), WhereText(next))
	add("Instructions", old.Instructions, next.Instructions)
	return strings.Join(lines, "\n")
}

// Save creates an automation or changes one, keeping what its
// runs recorded.
func (rn *Runner) Save(a Automation) (Automation, error) {
	return rn.SaveWithID(a, "")
}

// createID lets a phone retry an interrupted creation without adding it twice.
func (rn *Runner) SaveWithID(a Automation, createID string) (Automation, error) {
	now := rn.Now()
	i, exists := rn.Store.Find(a.ID)
	if !exists {
		a.ID, a.Created, a.Due = createID, now, now
	}
	if err := a.Normalize(); err != nil {
		return Automation{}, err
	}
	if exists {
		old := rn.Store.Automations[i]
		a.Created, a.Due = old.Created, old.Due
		if a.Schedule != old.Schedule || a.Enabled && !old.Enabled {
			a.Due = now
		}
		rn.keepTriggerStates(old, a)
		rn.Store.Automations[i] = a
	} else {
		rn.Store.Automations = append(rn.Store.Automations, a)
	}
	rn.Changed()
	return a, nil
}

// Changed saves the automations and keeps the login item to
// whether one is on.
func (rn *Runner) Changed() {
	rn.Persist()
	rn.syncWebhook()
	rn.syncRelay()
	rn.host.SyncLoginItem(rn.Enabled())
	rn.host.Changed()
}

func (rn *Runner) ListText() string {
	if len(rn.Store.Automations) == 0 {
		return "No automations yet."
	}
	var b strings.Builder
	for _, a := range rn.Store.Automations {
		state := "Paused"
		if a.Enabled {
			state = "Enabled"
			if a.Schedule != "" {
				state += "; next scheduled run: " + FormatTime(a.Next(rn.Now()))
			}
		}
		fmt.Fprintf(&b, "%s\"%s\"· %s · %s · %s", a.ID, a.Name, filepath.Base(a.Project), WakeText(a, rn.name), state)
		if r, ok := rn.Store.LastRun(a.ID); ok {
			fmt.Fprintf(&b, " · Last run: %s", ResultLabel(r.Result))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
