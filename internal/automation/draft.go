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
	// TriggerKind is what else wakes it: none, recipe, command, file, wake
	// or ingress. A recipe is named by TriggerRecipe and given
	// TriggerParams; a command is in TriggerCommand and wakes as
	// TriggerWhen says.
	TriggerKind    string       `json:"trigger_kind"`
	TriggerRecipe  string       `json:"trigger_recipe"`
	TriggerParams  []DraftParam `json:"trigger_params"`
	TriggerWhen    string       `json:"trigger_when"`
	TriggerPath    string       `json:"trigger_path"`
	TriggerCommand string       `json:"trigger_command"`
}

// DraftParam is the value a draft gives one of a recipe's parameters: a
// list of them, since a schema an agent answers to cannot have open keys.
type DraftParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Draft trigger kinds.
const (
	draftNoTrigger = "none"
	draftRecipe    = "recipe"
	draftCommand   = "command"
	draftFile      = "file"
	draftWake      = "wake"
	draftIngress   = "ingress"
)

const automationDraftSchema = `{"type":"object","additionalProperties":false,` +
	`"required":["name","schedule","check","rounds","instructions","time_missing","trigger_kind","trigger_recipe","trigger_params","trigger_when","trigger_path","trigger_command"],` +
	`"properties":{"name":{"type":"string"},"schedule":{"type":"string"},"check":{"type":"string"},` +
	`"rounds":{"type":"integer"},"instructions":{"type":"string"},"time_missing":{"type":"boolean"},` +
	`"trigger_kind":{"type":"string","enum":["none","recipe","command","file","wake","ingress"]},` +
	`"trigger_recipe":{"type":"string"},` +
	`"trigger_params":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["name","value"],"properties":{"name":{"type":"string"},"value":{"type":"string"}}}},` +
	`"trigger_when":{"type":"string","enum":["","success","changed","line"]},` +
	`"trigger_path":{"type":"string"},"trigger_command":{"type":"string"}}}`

// defaultAutomationSchedule is the time of a sentence that names none.
const defaultAutomationSchedule = "0 9 * * *"

func automationDraftPrompt(request, project string, now time.Time) string {
	zone, _ := now.Zone()
	weekdays := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	return fmt.Sprintf("Help Miya turn a request into a scheduled automation. Return only JSON that matches the schema. Do not perform any actions.\n\nCurrent time: %s %s (time zone %s)\nProject directory: %s\nTop-level project files: %s\n\nRules:\n- name: a short English name, 3–8 words, describing the task.\n- schedule: a 5-field cron expression (minute hour day month weekday) in local time. If the request specifies no time, use \"%s\" and set time_missing to true.\n- check: if the request requires a verifiable result such as passing tests or a successful build, provide one complete shell command to run in the project directory. Infer it from project files, for example go test ./... for go.mod or npm test for package.json. Do not include cd. Leave blank if no verification was requested.\n- rounds: maximum attempts when check is present, from 1 to 5. \"Try twice more\" means 3 rounds; default to 3. Use 1 when there is no check.\n- trigger_kind: what else wakes it, besides the schedule. recipe: a ready-made check; put its name in trigger_recipe and a value for each of its parameters in trigger_params. Prefer a recipe when one fits. The recipes: %s. command: anything else, as a shell command you write in trigger_command; Miya runs it in the project directory and does not know what it looks at, so thresholds and comparisons belong in the command. trigger_when says how it wakes. success: Miya runs it every few minutes and wakes when it exits 0; exit 1 means not now and any other status is an error, and the first line it prints is shown as what it found, as in test $(queue-depth) -gt 100. changed: Miya runs it every few minutes and wakes each time it prints something else than before, giving the run what it printed, as in gh issue list --limit 20 for 'when there are new issues'. line: Miya keeps it running and wakes with each line it prints, for a command that waits for events, as in tail -n 0 -F app.log | grep --line-buffered ERROR. Keep success and changed commands quick and free of side effects. file: when the file or directory in trigger_path (absolute, ~ allowed) changes. wake: when the computer wakes from sleep. ingress: when woken from outside, for example by a script or webhook. Otherwise none. Use empty strings and an empty list for the fields a kind does not need. When the request names a condition but no time, set time_missing to false and leave schedule empty: the trigger wakes it. When it names both a time and a condition that is met or not (a recipe or a success command), as in 'every day at 9, clean up if disk is low', give the schedule and the condition: it runs at that time only when the condition is met.\n- instructions: the first message sent to the coding agent for each run. Explain the task and completion criteria in English, and ask it to reply \"Nothing to report\" when there is no work. Do not include the schedule, check command, or retry rules; Miya manages those.\n\nRequest: %s",
		now.Format("2006-01-02 15:04"), weekdays[now.Weekday()], zone, project, projectTopFiles(project), defaultAutomationSchedule, RecipeHints(), request)
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
	for _, t := range a.Triggers {
		if t.Condition != nil && t.Condition.When == WhenLine {
			// Each line is an event: one that comes during a run waits for
			// the run to end rather than being lost.
			a.Policy.Queue = true
		}
	}
	return a, timeMissing, nil
}

// draftTrigger is the trigger a draft names, and whether it is a condition
// that is met or not, which a schedule can gate on.
func draftTrigger(d Draft) (*Trigger, bool) {
	path, command := strings.TrimSpace(d.TriggerPath), strings.TrimSpace(d.TriggerCommand)
	switch strings.TrimSpace(d.TriggerKind) {
	case draftRecipe:
		name := strings.TrimSpace(d.TriggerRecipe)
		if name == "" {
			return nil, false
		}
		params := map[string]string{}
		for _, p := range d.TriggerParams {
			if n := strings.TrimSpace(p.Name); n != "" {
				params[n] = strings.TrimSpace(p.Value)
			}
		}
		r, _ := FindRecipe(name)
		return &Trigger{Kind: TriggerCondition, Condition: &Condition{Recipe: name, Params: params}}, r.When == "" || r.When == WhenSuccess
	case draftCommand:
		if command == "" {
			return nil, false
		}
		when := strings.TrimSpace(d.TriggerWhen)
		if when != WhenChanged && when != WhenLine {
			when = WhenSuccess
		}
		// A change or a line is an event, not a state a schedule could
		// wait for.
		return &Trigger{Kind: TriggerCondition, Condition: &Condition{Command: command, When: when}}, when == WhenSuccess
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
