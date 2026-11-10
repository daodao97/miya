package automation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// name is the name of an automation, for the triggers that follow one.
func (rn *Runner) name(id string) string {
	if i, ok := rn.Store.Find(id); ok {
		return rn.Store.Automations[i].Name
	}
	return ""
}

// View is an automation as the pages show it, with what its triggers have
// seen and its latest wakes.
func (rn *Runner) View(a Automation, now time.Time) View {
	_, running := rn.Active[a.ID]
	v := ViewOf(a, append([]Run(nil), rn.Store.Runs[a.ID]...), running, now)
	v.ScheduleText = WakeText(a, rn.name)
	for _, t := range a.Triggers {
		v.TriggerTexts[t.ID] = DescribeTrigger(t, rn.name)
		if url := rn.TriggerURL(a, t); url != "" && !t.Disabled {
			if v.WakeURLs == nil {
				v.WakeURLs = map[string]string{}
			}
			v.WakeURLs[t.ID] = url
		}
		if st, ok := rn.Store.Triggers[TriggerKey(a.ID, t.ID)]; ok {
			if v.TriggerStates == nil {
				v.TriggerStates = map[string]TriggerState{}
			}
			v.TriggerStates[t.ID] = st
		}
	}
	v.Wakes = append([]Wake(nil), rn.Store.Wakes[a.ID]...)
	if a.HasTrigger(TriggerIngress) {
		v.WakeCommand = WakeCommand(a)
	}
	return v
}

// WakeCommand wakes an automation from a terminal.
func WakeCommand(a Automation) string {
	return "miya -wake " + a.ID
}

// PolicyText says how an automation's wakes are held back.
func PolicyText(p WakePolicy) string {
	var parts []string
	if p.Queue {
		parts = append(parts, "queue one wake while running")
	} else {
		parts = append(parts, "skip wakes while running")
	}
	if p.DebounceSeconds > 0 {
		parts = append(parts, fmt.Sprintf("gather wakes within %ds", p.DebounceSeconds))
	}
	if p.CooldownMinutes > 0 {
		parts = append(parts, fmt.Sprintf("at least %s between triggered runs", minutesText(p.CooldownMinutes)))
	}
	if p.MaxPerDay > 0 {
		parts = append(parts, "at most "+strconv.Itoa(p.MaxPerDay)+" triggered runs a day")
	}
	if p.Gate != nil {
		parts = append(parts, "only "+strings.ToLower(DescribeTrigger(Trigger{Kind: TriggerCondition, Condition: p.Gate}, nil)))
	}
	return strings.Join(parts, "; ")
}

func textOr(s, empty string) string {
	if s == "" {
		return empty
	}
	return s
}

// triggersText lists an automation's triggers, the ones that are off too.
func (rn *Runner) triggersText(a Automation) string {
	var parts []string
	for _, t := range a.Triggers {
		text := DescribeTrigger(t, rn.name)
		if t.Disabled {
			text += " (off)"
		}
		parts = append(parts, text)
	}
	return textOr(strings.Join(parts, "; "), "none")
}

func minutesText(m int) string {
	if m%60 == 0 {
		return strconv.Itoa(m/60) + "h"
	}
	return strconv.Itoa(m) + "m"
}

// keepTriggerStates drops what the triggers of an automation saw when they
// are gone or now look at something else, so that a changed condition
// starts afresh rather than counting as already fired.
func (rn *Runner) keepTriggerStates(old, next Automation) {
	if rn.Store.Triggers == nil {
		return
	}
	same := func(a, b Trigger) bool {
		a.Disabled, b.Disabled = false, false
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return string(x) == string(y)
	}
	for _, t := range old.Triggers {
		if n, ok := next.Trigger(t.ID); !ok || !same(t, n) {
			delete(rn.Store.Triggers, TriggerKey(old.ID, t.ID))
			if rn.wake != nil {
				delete(rn.wake.files, TriggerKey(old.ID, t.ID))
				delete(rn.wake.pending, TriggerKey(old.ID, t.ID))
			}
		}
	}
}

// Delete removes an automation and what it recorded. A run in progress
// goes on as an ordinary conversation.
func (rn *Runner) Delete(id string) {
	i, ok := rn.Store.Find(id)
	if !ok {
		return
	}
	rn.Store.Automations = append(rn.Store.Automations[:i], rn.Store.Automations[i+1:]...)
	rn.Store.Forget(id)
	delete(rn.Active, id)
	if rn.wake != nil {
		delete(rn.wake.queued, id)
		for k := range rn.wake.pending {
			if strings.HasPrefix(k, id+"/") {
				delete(rn.wake.pending, k)
			}
		}
	}
	rn.Changed()
}
