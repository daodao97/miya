package claude

import (
	"encoding/json"
	"errors"
	"fmt"

	"miya/internal/textutil"
)

// CLI is how this Miya runs Claude Code: one-off calls such as drafting an
// automation or extracting memories reuse it.
type CLI struct {
	Binary    string
	Args, Env []string
}

// StructuredOutput is the answer of a Claude Code --json-schema run. task
// says what the run was for, as its error reports: "draft the automation".
func StructuredOutput(out []byte, task string) ([]byte, error) {
	var r struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("Unable to parse Claude Code output: %v", err)
	}
	if r.IsError {
		return nil, errors.New("Claude Code could not " + task + ": " + textutil.ClipRunes(r.Result, 300))
	}
	if len(r.StructuredOutput) > 0 && string(r.StructuredOutput) != "null" {
		return r.StructuredOutput, nil
	}
	return []byte(r.Result), nil
}
