package automation

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/model"
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
}

type Request struct {
	ID         string      `json:"id,omitempty"`
	Automation *Automation `json:"automation,omitempty"`
	Enabled    bool        `json:"enabled,omitempty"`
	// Request, Project and Agent ask for a draft.
	Request string `json:"request,omitempty"`
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

func ViewOf(a Automation, runs []Run, running bool, now time.Time) View {
	v := View{Automation: a, ScheduleText: DescribeCron(a.Schedule), Running: running, Runs: runs}
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

// CardDetail is what the confirmation card shows of a new one.
func (rn *Runner) CardDetail(a Automation, now time.Time) string {
	return strings.Join([]string{
		fmt.Sprintf("Schedule: %s (%s); next run: %s", DescribeCron(a.Schedule), a.Schedule, FormatTime(a.Next(now))),
		"Project: " + a.Project,
		"Agent: " + model.AgentName(a.Agent),
		"Completion check: " + CheckText(a),
		"Instructions: " + a.Instructions,
	}, "\n")
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
	add("Schedule", DescribeCron(old.Schedule), DescribeCron(next.Schedule))
	add("Agent", model.AgentName(old.Agent), model.AgentName(next.Agent))
	add("Completion check", CheckText(old), CheckText(next))
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
	rn.host.SyncLoginItem(rn.Store.AnyEnabled())
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
			state = "Enabled; next run: " + FormatTime(a.Next(rn.Now()))
		}
		fmt.Fprintf(&b, "%s\"%s\"· %s · %s · %s", a.ID, a.Name, filepath.Base(a.Project), DescribeCron(a.Schedule), state)
		if r, ok := rn.Store.LastRun(a.ID); ok {
			fmt.Fprintf(&b, " · Last run: %s", ResultLabel(r.Result))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
