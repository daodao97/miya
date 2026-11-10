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
	// TriggerKind is what else wakes it: none, disk_free, memory_free,
	// command, file, wake or ingress, with the threshold, path or command
	// it needs.
	TriggerKind        string  `json:"trigger_kind"`
	TriggerThresholdGB float64 `json:"trigger_threshold_gb"`
	TriggerPath        string  `json:"trigger_path"`
	TriggerCommand     string  `json:"trigger_command"`
}

// Draft trigger kinds besides the probes.
const (
	draftNoTrigger = "none"
	draftFile      = "file"
	draftWake      = "wake"
	draftIngress   = "ingress"
)

const automationDraftSchema = `{"type":"object","additionalProperties":false,` +
	`"required":["name","schedule","check","rounds","instructions","time_missing","trigger_kind","trigger_threshold_gb","trigger_path","trigger_command"],` +
	`"properties":{"name":{"type":"string"},"schedule":{"type":"string"},"check":{"type":"string"},` +
	`"rounds":{"type":"integer"},"instructions":{"type":"string"},"time_missing":{"type":"boolean"},` +
	`"trigger_kind":{"type":"string","enum":["none","disk_free","memory_free","command","file","wake","ingress"]},` +
	`"trigger_threshold_gb":{"type":"number"},"trigger_path":{"type":"string"},"trigger_command":{"type":"string"}}}`

// defaultAutomationSchedule is the time of a sentence that names none.
const defaultAutomationSchedule = "0 9 * * *"

func automationDraftPrompt(request, project string, now time.Time) string {
	zone, _ := now.Zone()
	weekdays := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	return fmt.Sprintf("Help Miya turn a request into a scheduled automation. Return only JSON that matches the schema. Do not perform any actions.\n\nCurrent time: %s %s (time zone %s)\nProject directory: %s\nTop-level project files: %s\n\nRules:\n- name: a short English name, 3–8 words, describing the task.\n- schedule: a 5-field cron expression (minute hour day month weekday) in local time. If the request specifies no time, use \"%s\" and set time_missing to true.\n- check: if the request requires a verifiable result such as passing tests or a successful build, provide one complete shell command to run in the project directory. Infer it from project files, for example go test ./... for go.mod or npm test for package.json. Do not include cd. Leave blank if no verification was requested.\n- rounds: maximum attempts when check is present, from 1 to 5. \"Try twice more\" means 3 rounds; default to 3. Use 1 when there is no check.\n- trigger_kind: what else wakes it, besides the schedule. disk_free: when free disk space falls below trigger_threshold_gb on the volume in trigger_path (\"/\" unless one is named), as in \"when storage is below 5G\". memory_free: when available memory falls below trigger_threshold_gb. command: when trigger_command, a shell command, exits 0. file: when the file or directory in trigger_path (absolute, ~ allowed) changes. wake: when the computer wakes from sleep. ingress: when woken from outside, for example by a script or webhook. Otherwise none. Use 0 and empty strings for the fields a kind does not need. When the request names a condition but no time, set time_missing to false and leave schedule empty: the trigger wakes it. When it names both a time and a condition, as in \"every day at 9, clean up if disk is low\", give the schedule and the condition: it runs at that time only when the condition is met.\n- instructions: the first message sent to the coding agent for each run. Explain the task and completion criteria in English, and ask it to reply \"Nothing to report\" when there is no work. Do not include the schedule, check command, or retry rules; Miya manages those.\n\nRequest: %s",
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
	trigger, condition := draftTrigger(d)
	timeMissing := d.TimeMissing || strings.TrimSpace(d.Schedule) == ""
	switch {
	case trigger != nil && timeMissing:
		// The trigger wakes it; a time it did not name would add runs.
		a.Schedule, timeMissing = "", false
		a.Triggers = []Trigger{*trigger}
	case condition:
		// At a time, if the condition holds: the condition gates the runs.
		a.Policy.Gate = trigger.Condition
	case trigger != nil:
		a.Triggers = []Trigger{*trigger}
	case timeMissing:
		a.Schedule = defaultAutomationSchedule
	}
	if a.Schedule != "" {
		if _, err := ParseCron(a.Schedule); err != nil {
			return Automation{}, false, fmt.Errorf("The generated schedule %q is invalid. Describe the time more precisely.", a.Schedule)
		}
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

// draftTrigger is the trigger a draft names, and whether it is a condition,
// which a schedule can gate on.
func draftTrigger(d Draft) (*Trigger, bool) {
	path := strings.TrimSpace(d.TriggerPath)
	switch kind := strings.TrimSpace(d.TriggerKind); kind {
	case ProbeDiskFree, ProbeMemoryFree:
		if d.TriggerThresholdGB <= 0 {
			return nil, false
		}
		c := &Condition{Probe: kind, BelowGB: d.TriggerThresholdGB}
		if kind == ProbeDiskFree {
			c.Path = path
		}
		return &Trigger{Kind: TriggerCondition, Condition: c}, true
	case ProbeCommand:
		if strings.TrimSpace(d.TriggerCommand) == "" {
			return nil, false
		}
		return &Trigger{Kind: TriggerCondition, Condition: &Condition{Probe: ProbeCommand, Command: strings.TrimSpace(d.TriggerCommand)}}, true
	case draftFile:
		if path == "" {
			return nil, false
		}
		return &Trigger{Kind: TriggerFile, Path: path}, false
	case draftWake:
		return &Trigger{Kind: TriggerSystem, Event: SystemWake}, false
	case draftIngress:
		return &Trigger{Kind: TriggerIngress}, false
	}
	return nil, false
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
