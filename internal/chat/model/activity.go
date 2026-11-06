package model

import "encoding/json"

// Activity is the common tool/message metadata consumed by conversation
// features. The private wire data preserves additional fields from old caches
// and services; neither models nor renderers interpret provider envelopes.
type Activity struct {
	Phase          string          `json:"phase,omitempty"`
	Server         string          `json:"server"`
	Tool           string          `json:"tool"`
	Arguments      json.RawMessage `json:"arguments"`
	Command        string          `json:"command"`
	Query          string          `json:"query"`
	CommandActions []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Path  string `json:"path"`
		Query string `json:"query"`
	} `json:"commandActions"`
	Changes []struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	} `json:"changes"`
	wire json.RawMessage
}

type activityFields Activity

func (a *Activity) UnmarshalJSON(data []byte) error {
	var fields activityFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*a = Activity(fields)
	a.wire = append(json.RawMessage(nil), data...)
	return nil
}

func (a Activity) MarshalJSON() ([]byte, error) {
	fields, err := json.Marshal(activityFields(a))
	if err != nil || len(a.wire) == 0 {
		return fields, err
	}
	var original, current map[string]json.RawMessage
	if err := json.Unmarshal(a.wire, &original); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(fields, &current); err != nil {
		return nil, err
	}
	if original == nil {
		original = map[string]json.RawMessage{}
	}
	// An explicitly cleared optional phase must not retain its cached value.
	delete(original, "phase")
	for name, value := range current {
		original[name] = value
	}
	return json.Marshal(original)
}

type PlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}
