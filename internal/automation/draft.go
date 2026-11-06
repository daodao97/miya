package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"miya/internal/textutil"
)

// An automation is written as one sentence; the person's agent turns it into
// settings in one call with no tools and a JSON schema for its answer, then
// Miya checks what it made.

type Draft struct {
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

// DraftFor asks agent to turn request into settings. It runs
// programs and waits for them: call it off the main thread.
func DraftFor(ctx context.Context, generate DraftGenerator, project, request string, now time.Time) (Draft, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	prompt := automationDraftPrompt(request, project, now)
	raw, err := generate(ctx, project, prompt, automationDraftSchema)
	if err != nil {
		return Draft{}, err
	}
	var d Draft
	if err := json.Unmarshal(raw, &d); err != nil {
		return Draft{}, fmt.Errorf("The agent returned an unexpected format: %v", err)
	}
	return d, nil
}

// FromDraft checks what the agent made and fills the rest.
func FromDraft(d Draft, agent, project, request string) (Automation, bool, error) {
	a := Automation{Name: d.Name, Project: project, Agent: agent, Request: strings.TrimSpace(request),
		Instructions: d.Instructions, Schedule: d.Schedule, Check: d.Check, Rounds: d.Rounds}
	a.Check = stripProjectCd(a.Check, project)
	timeMissing := d.TimeMissing || strings.TrimSpace(d.Schedule) == ""
	if timeMissing {
		a.Schedule = defaultAutomationSchedule
	}
	if _, err := ParseCron(a.Schedule); err != nil {
		return Automation{}, false, fmt.Errorf("The generated schedule %q is invalid. Describe the time more precisely.", a.Schedule)
	}
	if strings.TrimSpace(a.Instructions) == "" {
		a.Instructions = a.Request
	}
	if strings.TrimSpace(a.Name) == "" {
		a.Name = textutil.ClipRunes(textutil.FirstLine(a.Request), 14)
	}
	if err := a.Normalize(); err != nil {
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

// DraftGenerator returns structured output without executing the drafted automation.
type DraftGenerator func(context.Context, string, string, string) ([]byte, error)
