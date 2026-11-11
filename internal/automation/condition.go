package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"miya/internal/textutil"
)

// A condition is a shell command. Miya does not know what the command looks
// at: it runs the command and the command says when to wake the automation.
// Thresholds, units and comparisons live in the command, so a new kind of
// task needs a new command, or a recipe for one (recipe.go), and no new
// version of Miya.

// How a condition's command wakes the automation.
const (
	// WhenSuccess runs the command every few minutes. It is met when the
	// command exits 0 and not met when it exits 1; any other status is an
	// error. The first line it prints is shown as what it found.
	WhenSuccess = "success"
	// WhenChanged runs the command every few minutes and wakes each time it
	// prints something else than it did before, with what it printed.
	WhenChanged = "changed"
	// WhenLine keeps the command running and wakes with each line it
	// prints (stream.go).
	WhenLine = "line"
)

// What conditions were called before they were commands. A stored
// condition that names disk_free or memory_free is read as the recipe of
// that name; command only said that the condition has a command.
const (
	ProbeDiskFree   = "disk_free"
	ProbeMemoryFree = "memory_free"
	ProbeCommand    = "command"
)

// Condition is a command and how it wakes.
type Condition struct {
	// Recipe names the recipe the command comes from and Params are the
	// values of its parameters, which the command gets as environment
	// variables. Command, When and Text are copied from the recipe when the
	// automation is saved, so what was confirmed is what runs.
	Recipe string `json:"recipe,omitempty"`
	Params Params `json:"params,omitempty"`
	// Command runs in a login shell in the project.
	Command string `json:"command,omitempty"`
	// When is success, changed or line; success when empty.
	When string `json:"when,omitempty"`
	// Text says when a recipe's condition is met, with $NAME for each
	// parameter; a command written by hand is shown as it is.
	Text string `json:"text,omitempty"`
	// EveryMinutes is how often a success or changed command runs.
	EveryMinutes int `json:"everyMinutes,omitempty"`
	// RepeatHours is how long a condition that stays met waits before it
	// wakes the automation again.
	RepeatHours int `json:"repeatHours,omitempty"`
	// Probe, Path and BelowGB are how conditions were written before
	// recipes; normalize turns them into a recipe and its parameters.
	Probe   string  `json:"probe,omitempty"`
	Path    string  `json:"path,omitempty"`
	BelowGB float64 `json:"belowGB,omitempty"`
}

// Params are the values of a recipe's parameters. Agents write an amount as
// a number as often as a string: both are read.
type Params map[string]string

func (p *Params) UnmarshalJSON(b []byte) error {
	var raw map[string]flexString
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw == nil {
		*p = nil
		return nil
	}
	out := Params{}
	for name, value := range raw {
		out[name] = string(value)
	}
	*p = out
	return nil
}

// normalize checks a condition and fills its defaults.
func (c *Condition) normalize() error {
	c.fromProbe()
	c.Recipe, c.Command, c.When = strings.TrimSpace(c.Recipe), strings.TrimSpace(c.Command), strings.TrimSpace(c.When)
	if c.Recipe != "" {
		if err := c.applyRecipe(); err != nil {
			return err
		}
	} else {
		c.Params, c.Text = nil, ""
	}
	if c.Command == "" {
		return errors.New("The condition needs a command or a recipe")
	}
	switch c.When {
	case "":
		c.When = WhenSuccess
	case WhenSuccess, WhenChanged, WhenLine:
	default:
		return fmt.Errorf("Unknown rule %q: use success, changed or line", c.When)
	}
	if c.When == WhenLine {
		c.EveryMinutes, c.RepeatHours = 0, 0
		return nil
	}
	if c.EveryMinutes <= 0 {
		c.EveryMinutes = defaultProbeEvery
	}
	c.EveryMinutes = min(c.EveryMinutes, 24*60)
	if c.RepeatHours <= 0 {
		c.RepeatHours = defaultProbeRepeat
	}
	return nil
}

// conditionText says when a condition wakes; translate puts a recipe's text
// in the person's language, for the pages rather than for an agent.
func conditionText(c Condition, translate bool) string {
	if c.Command == "" {
		_ = c.normalize()
	}
	if c.Text != "" {
		text := c.Text
		if translate {
			text = tr(text)
		}
		return os.Expand(text, func(name string) string { return c.Params[name] })
	}
	switch c.When {
	case WhenChanged:
		return fmt.Sprintf(tr("When the output of `%s` changes"), c.Command)
	case WhenLine:
		return fmt.Sprintf(tr("When `%s` prints a line"), c.Command)
	}
	return fmt.Sprintf(tr("When `%s` succeeds"), c.Command)
}

func describeCondition(c Condition) string { return conditionText(c, true) }

// ProbeResult is what running a condition's command found.
type ProbeResult struct {
	Met bool
	// Value is the first line the command printed, as people read it.
	Value string
	// Output is everything it printed, for the conditions that wake when
	// that changes.
	Output string
	Err    string
}

// probeTimeout bounds a success or changed command.
const probeTimeout = 30 * time.Second

// lineTryTime is how long a line command has to show that it stays up.
const lineTryTime = 5 * time.Second

// textFingerprint identifies what a command printed, to compare with what
// it printed before.
func textFingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// runProbe runs a success or changed condition's command once, with dir as
// its working directory.
func runProbe(c Condition, dir string) ProbeResult {
	if c.Command == "" {
		// A condition stored before recipes, not saved since.
		if err := c.normalize(); err != nil {
			return ProbeResult{Err: err.Error()}
		}
	}
	if c.When == WhenLine {
		return ProbeResult{Err: "a command that keeps running cannot be checked at a moment"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := shellCommand(ctx, dir, c).Output()
	text := strings.TrimSpace(string(out))
	value := textutil.ClipRunes(textutil.FirstLine(text), 200)
	if ctx.Err() == context.DeadlineExceeded {
		return ProbeResult{Err: fmt.Sprintf("stopped after %s", probeTimeout)}
	}
	failed := false
	if err != nil {
		exit, exited := err.(*exec.ExitError)
		if !exited {
			return ProbeResult{Err: err.Error()}
		}
		failed = true
		// Exit 1 answers "not now". Anything else, and any failure of a
		// command whose output is what matters, found nothing out.
		if c.When == WhenChanged || exit.ExitCode() != 1 {
			why := textutil.FirstNonEmpty(textutil.FirstLine(strings.TrimSpace(string(exit.Stderr))), value, err.Error())
			return ProbeResult{Err: textutil.ClipRunes(fmt.Sprintf("the command failed (%v): %s", err, why), 300)}
		}
	}
	if c.When == WhenChanged {
		return ProbeResult{Value: value, Output: clipData(text)}
	}
	return ProbeResult{Met: !failed, Value: value}
}

// TryCondition runs a condition's command once before it is saved, to show
// that it works: a success or changed command as a probe runs it, a line
// command until it prints a line or has stayed up for lineTryTime. Err says
// why the command cannot be used.
func TryCondition(c Condition, dir string) ProbeResult {
	if err := c.normalize(); err != nil {
		return ProbeResult{Err: err.Error()}
	}
	if c.When != WhenLine {
		return runProbe(c, dir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), lineTryTime)
	defer cancel()
	first := ""
	err := streamOnce(ctx, dir, c, func(ev StreamEvent) {
		if ev.Line != "" && first == "" {
			first = ev.Line
			cancel()
		}
	})
	if ctx.Err() == nil {
		// It exited by itself: it would be started again and again.
		return ProbeResult{Err: err.Error()}
	}
	return ProbeResult{Met: true, Value: textutil.ClipRunes(textutil.FirstLine(first), 200), Output: first}
}
