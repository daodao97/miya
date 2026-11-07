package interaction

import (
	"encoding/json"
	"testing"
)

func TestEmptyFormConfirmationSurvivesJSON(t *testing.T) {
	request := Request{Category: Form, Mode: "form", RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`)}
	data, err := json.Marshal(Response{Action: Allow, Content: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var reply Response
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResponse(request, reply); err != nil {
		t.Fatalf("empty confirmation lost in transit: %s: %v", data, err)
	}
	if err := ValidateResponse(request, Response{Action: Allow}); err == nil {
		t.Fatal("missing form content was accepted")
	}
	request.RequestedSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	if err := ValidateResponse(request, reply); err == nil {
		t.Fatal("required fields were bypassed by an empty confirmation")
	}
}
