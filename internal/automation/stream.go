package automation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"miya/internal/notification"
	"miya/internal/textutil"
)

// A line trigger keeps a command running and wakes its automation with each
// line the command prints: a subscription to events, a log being followed, a
// file watcher. Miya starts the command, starts it again when it exits, and
// stops it with its children when the trigger goes away or the service ends.
// What the command listens to is its own business.

const (
	// streamLinesPerMinute is how many lines a minute become events; the
	// rest are dropped and counted.
	streamLinesPerMinute = 30
	// A command that exits is started again after streamRestartMin, twice as
	// long each time up to streamRestartMax; one that ran for streamHealthy
	// starts over at the shortest wait.
	streamRestartMin = 5 * time.Second
	streamRestartMax = 5 * time.Minute
	streamHealthy    = time.Minute
	// defaultLineDebounce gathers the lines printed together into one wake.
	defaultLineDebounce = 5
	// groupGrace is how long a stopped command has to exit by itself.
	groupGrace = 2 * time.Second
)

// StreamEvent is what a running line trigger reports: that it started, a
// line it printed, or why it stopped.
type StreamEvent struct {
	Started bool
	Line    string
	// Missed is how many lines before this one were dropped.
	Missed int
	Err    string
	// Ran is how long the command ran before it stopped.
	Ran time.Duration
}

// shellCommand is a condition's command in a login shell in dir, given the
// condition's parameters as environment variables and a process group of
// its own, so that stopping it leaves nothing behind.
func shellCommand(ctx context.Context, dir string, c Condition) *exec.Cmd {
	cmd := exec.CommandContext(ctx, textutil.FirstNonEmpty(os.Getenv("SHELL"), "/bin/sh"), "-lc", c.Command)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		cmd.Dir = dir
	}
	if len(c.Params) > 0 {
		cmd.Env = os.Environ()
		for name, value := range c.Params {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	ownGroup(cmd)
	cmd.WaitDelay = groupGrace + time.Second
	return cmd
}

// commandLabel names a condition's command in a line of text.
func commandLabel(c Condition) string {
	if c.Recipe != "" {
		return c.Recipe
	}
	return textutil.ClipRunes(textutil.FirstLine(c.Command), 80)
}

// lineLimiter lets streamLinesPerMinute lines a minute through.
type lineLimiter struct {
	window    time.Time
	n, missed int
}

// allow reports whether a line gets through, and how many before it did not.
func (l *lineLimiter) allow(now time.Time) (int, bool) {
	if now.Sub(l.window) >= time.Minute {
		l.window, l.n = now, 0
	}
	if l.n >= streamLinesPerMinute {
		l.missed++
		return 0, false
	}
	l.n++
	missed := l.missed
	l.missed = 0
	return missed, true
}

// tailBuffer keeps the end of what a command wrote to its standard error.
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = append([]byte(nil), t.b[len(t.b)-4096:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) lastLine() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.b)), "\n")
	return textutil.ClipRunes(strings.TrimSpace(lines[len(lines)-1]), 200)
}

// runStream keeps a line trigger's command running until ctx ends, and
// reports what it does to emit, which is called from this goroutine.
func runStream(ctx context.Context, dir string, c Condition, emit func(StreamEvent)) {
	wait := streamRestartMin
	for ctx.Err() == nil {
		started := time.Now()
		err := streamOnce(ctx, dir, c, emit)
		if ctx.Err() != nil {
			return
		}
		ran := time.Since(started)
		if ran >= streamHealthy {
			wait = streamRestartMin
		}
		emit(StreamEvent{Err: fmt.Sprintf("%v; starting it again in %s", err, wait), Ran: ran})
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, streamRestartMax)
	}
}

// streamOnce runs the command once and reports its lines; it returns why
// the command stopped.
func streamOnce(parent context.Context, dir string, c Condition, emit func(StreamEvent)) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	cmd := shellCommand(ctx, dir, c)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	emit(StreamEvent{Started: true})
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var limit lineLimiter
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if missed, ok := limit.allow(time.Now()); ok {
			emit(StreamEvent{Line: clipData(line), Missed: missed})
		}
	}
	if sc.Err() != nil {
		// A line too long to read: nothing more can be read after it.
		cancel()
	}
	err = cmd.Wait()
	why := stderr.lastLine()
	switch {
	case sc.Err() != nil:
		return fmt.Errorf("stopped reading its output: %v", sc.Err())
	case err == nil && why == "":
		return errors.New("the command exited")
	case err == nil:
		return fmt.Errorf("the command exited: %s", why)
	case why != "":
		return fmt.Errorf("the command failed (%v): %s", err, why)
	}
	return fmt.Errorf("the command failed: %v", err)
}

// streamProc is a line trigger's running command; sig tells when the
// trigger changed and the command has to be started afresh.
type streamProc struct {
	sig    string
	cancel context.CancelFunc
}

// syncStreams runs the commands of the line triggers that are on and stops
// the ones whose trigger is gone, off or changed.
func (rn *Runner) syncStreams() {
	w := rn.wakes()
	want := map[string]bool{}
	for _, a := range rn.Store.Automations {
		if !a.Enabled {
			continue
		}
		for _, t := range a.Triggers {
			if t.Disabled || t.Kind != TriggerCondition || t.Condition == nil || t.Condition.When != WhenLine {
				continue
			}
			key := TriggerKey(a.ID, t.ID)
			data, _ := json.Marshal(t.Condition)
			sig := a.Project + "\x00" + string(data)
			want[key] = true
			if s := w.streams[key]; s != nil {
				if s.sig == sig {
					continue
				}
				s.cancel()
			}
			ctx, cancel := context.WithCancel(context.Background())
			s := &streamProc{sig: sig, cancel: cancel}
			w.streams[key] = s
			id, tid, c, dir, run := a.ID, t.ID, *t.Condition, a.Project, rn.Stream
			go run(ctx, dir, c, func(ev StreamEvent) {
				rn.host.Dispatch(func() {
					// What a stopped command still had to say is dropped.
					if rn.wakes().streams[key] == s {
						rn.afterLine(id, tid, ev)
					}
				})
			})
		}
	}
	for key, s := range w.streams {
		if !want[key] {
			s.cancel()
			delete(w.streams, key)
		}
	}
}

// stopStreams stops every line trigger's command, as the service ends.
func (rn *Runner) stopStreams() {
	if rn.wake == nil {
		return
	}
	for key, s := range rn.wake.streams {
		s.cancel()
		delete(rn.wake.streams, key)
	}
}

// afterLine records what a line trigger's command did and wakes the
// automation with each line it printed.
func (rn *Runner) afterLine(id, tid string, ev StreamEvent) {
	i, ok := rn.Store.Find(id)
	if !ok {
		return
	}
	a := rn.Store.Automations[i]
	t, ok := a.Trigger(tid)
	if !ok || t.Condition == nil {
		return
	}
	key, now := TriggerKey(id, tid), rn.Now()
	if rn.Store.Triggers == nil {
		rn.Store.Triggers = map[string]TriggerState{}
	}
	st := rn.Store.Triggers[key]
	st.LastProbe = now
	switch {
	case ev.Err != "":
		// A command that ran well before it stopped starts the count again:
		// only one that keeps failing is worth a notice.
		if ev.Ran >= streamHealthy {
			st.Errors = 0
		}
		st.Errors++
		st.Met, st.Error = false, textutil.ClipRunes(ev.Err, 300)
	case ev.Started:
		st.Met, st.Error = true, ""
	default:
		st.Met, st.Error, st.Errors = true, "", 0
		st.Value, st.LastFired = textutil.ClipRunes(textutil.FirstLine(ev.Line), 200), now
	}
	rn.Store.Triggers[key] = st
	rn.Persist()
	rn.host.Changed()
	switch {
	case ev.Err != "":
		if st.Errors == probeErrorNotice {
			rn.host.Deliver(notification.Notice{ID: "miya-auto-probe-" + key, Title: a.Name + " · " + tr("Condition check failing"), Body: st.Error})
		}
	case ev.Line != "":
		reason := fmt.Sprintf("`%s` printed: %s", commandLabel(*t.Condition), st.Value)
		if ev.Missed > 0 {
			reason = fmt.Sprintf("%d earlier lines were dropped (over %d a minute). %s", ev.Missed, streamLinesPerMinute, reason)
		}
		_, _ = rn.Wake(WakeEvent{Automation: id, Trigger: tid, Source: TriggerCondition, Reason: reason, Data: ev.Line})
	}
}
