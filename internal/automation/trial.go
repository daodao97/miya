package automation

import "fmt"

// Before an automation is saved, the command of each of its conditions is
// run once: a command nobody ran is a guess, and one that cannot run would
// only show as a failing check days later.

// Trial is what running a condition's command once found.
type Trial struct {
	// Text says what the condition is and When how it wakes; Met and Value
	// are what its command found.
	Text  string `json:"text"`
	When  string `json:"when,omitempty"`
	Met   bool   `json:"met,omitempty"`
	Value string `json:"value,omitempty"`
}

// GateTrial is the key of the gate's trial among an automation's.
const GateTrial = "gate"

// Result says in a line what the trial found, for an agent or a card.
func (t Trial) Result() string {
	text := "not met right now"
	switch {
	case t.When == WhenLine:
		text = "the command stays running"
	case t.When == WhenChanged:
		text = "the command runs"
	case t.Met:
		text = "met right now"
	}
	if t.Value != "" {
		text += ": " + t.Value
	}
	return text
}

// HasConditions reports whether a has a condition whose command a test run
// would run.
func (a Automation) HasConditions() bool {
	for _, t := range a.Triggers {
		if t.Kind == TriggerCondition && t.Condition != nil && !t.Disabled {
			return true
		}
	}
	return a.Policy.Gate != nil
}

// TryConditions runs the command of each condition of a once: those of its
// triggers that are on, by trigger ID, and its gate, as GateTrial. The
// error says which command cannot be used and why. It runs programs and
// waits for them: call it off the main thread.
func TryConditions(a Automation) (map[string]Trial, error) {
	trials := map[string]Trial{}
	try := func(key string, c Condition) error {
		res := TryCondition(c, a.Project)
		text := conditionText(c, false)
		if res.Err != "" {
			return fmt.Errorf("The test run of the trigger %q failed: %s", text, res.Err)
		}
		trials[key] = Trial{Text: text, When: c.When, Met: res.Met, Value: res.Value}
		return nil
	}
	for _, t := range a.Triggers {
		if t.Kind != TriggerCondition || t.Condition == nil || t.Disabled {
			continue
		}
		if err := try(t.ID, *t.Condition); err != nil {
			return nil, err
		}
	}
	if g := a.Policy.Gate; g != nil {
		if err := try(GateTrial, *g); err != nil {
			return nil, err
		}
	}
	if len(trials) == 0 {
		return nil, nil
	}
	return trials, nil
}
