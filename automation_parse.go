package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"miya/internal/claude"
	"miya/internal/codex"
)

// An automation is written as one sentence; the person's agent turns it into
// settings in one call with no tools and a JSON schema for its answer, then
// Miya checks what it made.

type automationDraft struct {
	Name         string `json:"name"`
	Schedule     string `json:"schedule"`
	Check        string `json:"check"`
	Rounds       int    `json:"rounds"`
	Instructions string `json:"instructions"`
	TimeMissing  bool   `json:"time_missing"`
}

const automationDraftSchema = `{"type":"object","additionalProperties":false,` +
	`"required":["name","schedule","check","rounds","instructions","time_missing"],` +
	`"properties":{"name":{"type":"string"},"schedule":{"type":"string"},"check":{"type":"string"},` +
	`"rounds":{"type":"integer"},"instructions":{"type":"string"},"time_missing":{"type":"boolean"}}}`

// defaultAutomationSchedule is the time of a sentence that names none.
const defaultAutomationSchedule = "0 9 * * *"

func automationDraftPrompt(request, project string, now time.Time) string {
	zone, _ := now.Zone()
	weekdays := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	return fmt.Sprintf("Help Miya turn a request into a scheduled automation. Return only JSON that matches the schema. Do not perform any actions.\n\nCurrent time: %s %s (time zone %s)\nProject directory: %s\nTop-level project files: %s\n\nRules:\n- name: a short English name, 3–8 words, describing the task.\n- schedule: a 5-field cron expression (minute hour day month weekday) in local time. If the request specifies no time, use \"%s\" and set time_missing to true.\n- check: if the request requires a verifiable result such as passing tests or a successful build, provide one complete shell command to run in the project directory. Infer it from project files, for example go test ./... for go.mod or npm test for package.json. Do not include cd. Leave blank if no verification was requested.\n- rounds: maximum attempts when check is present, from 1 to 5. \"Try twice more\" means 3 rounds; default to 3. Use 1 when there is no check.\n- instructions: the first message sent to the coding agent for each run. Explain the task and completion criteria in English, and ask it to reply \"Nothing to report\" when there is no work. Do not include the schedule, check command, or retry rules; Miya manages those.\n\nRequest: %s",
		now.Format("2006-01-02 15:04"), weekdays[now.Weekday()], zone, project, projectTopFiles(project), defaultAutomationSchedule, request)
}

// projectTopFiles lists a project's top-level entries, which tell its
// language and build tools.
func projectTopFiles(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "(unable to read)"
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && name != ".github" {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 40 {
		names = append(names[:40], "…")
	}
	if len(names) == 0 {
		return "(empty)"
	}
	return strings.Join(names, ", ")
}

// draftConfig is how this Miya runs Claude Code, which the call reuses.
type draftConfig struct {
	claudeBinary          string
	claudeArgs, claudeEnv []string
}

// draftAutomation asks agent to turn request into settings. It runs
// programs and waits for them: call it off the main thread.
func draftAutomation(ctx context.Context, cfg draftConfig, agent, project, request string, now time.Time) (automationDraft, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	prompt := automationDraftPrompt(request, project, now)
	var raw []byte
	var err error
	if agent == agentClaude {
		raw, err = draftWithClaude(ctx, cfg, project, prompt)
	} else {
		raw, err = draftWithCodex(ctx, project, prompt)
	}
	if err != nil {
		return automationDraft{}, err
	}
	var d automationDraft
	if err := json.Unmarshal(raw, &d); err != nil {
		return automationDraft{}, fmt.Errorf("The agent returned an unexpected format: %v", err)
	}
	return d, nil
}

func draftWithClaude(ctx context.Context, cfg draftConfig, project, prompt string) ([]byte, error) {
	bin := cfg.claudeBinary
	if bin == "" {
		var err error
		if bin, err = claude.Binary(); err != nil {
			return nil, err
		}
	}
	args := append(append([]string(nil), cfg.claudeArgs...), "-p", prompt, "--json-schema", automationDraftSchema, "--output-format", "json", "--tools", "")
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = project
	cmd.Env = append(os.Environ(), cfg.claudeEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Claude Code could not draft the automation: %v %s", err, clipRunes(strings.TrimSpace(stderr.String()), 300))
	}
	return claudeStructuredOutput(out)
}

// claudeStructuredOutput is the answer of a Claude Code --json-schema run.
func claudeStructuredOutput(out []byte) ([]byte, error) {
	var r struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("Unable to parse Claude Code output: %v", err)
	}
	if r.IsError {
		return nil, errors.New("Claude Code could not draft the automation: " + clipRunes(r.Result, 300))
	}
	if len(r.StructuredOutput) > 0 && string(r.StructuredOutput) != "null" {
		return r.StructuredOutput, nil
	}
	return []byte(r.Result), nil
}

func draftWithCodex(ctx context.Context, project, prompt string) ([]byte, error) {
	bin, err := codex.Binary()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "miya-draft-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	schema, last := filepath.Join(dir, "schema.json"), filepath.Join(dir, "last.json")
	if err := os.WriteFile(schema, []byte(automationDraftSchema), 0o600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--output-schema", schema, "-o", last, prompt)
	cmd.Dir = project
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Codex could not draft the automation: %v %s", err, clipRunes(lastLine(stderr.String()), 300))
	}
	return os.ReadFile(last)
}

// automationFromDraft checks what the agent made and fills the rest.
func automationFromDraft(d automationDraft, agent, project, request string) (Automation, bool, error) {
	a := Automation{Name: d.Name, Project: project, Agent: agent, Request: strings.TrimSpace(request),
		Instructions: d.Instructions, Schedule: d.Schedule, Check: d.Check, Rounds: d.Rounds}
	a.Check = stripProjectCd(a.Check, project)
	timeMissing := d.TimeMissing || strings.TrimSpace(d.Schedule) == ""
	if timeMissing {
		a.Schedule = defaultAutomationSchedule
	}
	if _, err := parseCron(a.Schedule); err != nil {
		return Automation{}, false, fmt.Errorf("The generated schedule %q is invalid. Describe the time more precisely.", a.Schedule)
	}
	if strings.TrimSpace(a.Instructions) == "" {
		a.Instructions = a.Request
	}
	if strings.TrimSpace(a.Name) == "" {
		a.Name = clipRunes(firstLine(a.Request), 14)
	}
	if err := a.normalize(); err != nil {
		return Automation{}, false, err
	}
	return a, timeMissing, nil
}

// stripProjectCd drops a leading cd into the project, where checks run
// anyway.
func stripProjectCd(check, project string) string {
	check = strings.TrimSpace(check)
	for _, dir := range []string{project, "'" + project + "'", `"` + project + `"`, "."} {
		if rest, ok := strings.CutPrefix(check, "cd "+dir+" && "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return check
}
