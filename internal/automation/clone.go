package automation

import (
	"maps"
	"slices"
)

// Clone copies mutable trigger and condition data before validation or editing.
func Clone(a Automation) Automation {
	condition := func(c *Condition) *Condition {
		if c == nil {
			return nil
		}
		copy := *c
		copy.Params = maps.Clone(c.Params)
		return &copy
	}
	a.Triggers = slices.Clone(a.Triggers)
	for i := range a.Triggers {
		t := &a.Triggers[i]
		t.Condition = condition(t.Condition)
		t.Match = maps.Clone(t.Match)
		t.Results = slices.Clone(t.Results)
	}
	a.Policy.Gate = condition(a.Policy.Gate)
	return a
}
