package automation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"miya/internal/chat/model"
	"miya/internal/chat/subagent"
	"miya/internal/notification"
	"miya/internal/textutil"
)

// Host is what the runner needs from the controller whose conversations
// carry the runs: the background service's.
type Host interface {
	// Dispatch runs f on the controller's thread.
	Dispatch(f func())
	// Submit sends a message, starting a conversation when ThreadID is 0,
	// and returns the conversation or 0 when its agent is not ready.
	Submit(model.SendRequest) int
	Thread(id int) model.Thread
	UpdateThread(id int, f func(*model.Thread))
	// AwaitsUser reports whether a conversation waits for an approval or an
	// answer.
	AwaitsUser(model.Thread) bool
	// Outcome is how a conversation's latest turn ended, as a sub-agent
	// status, and its last reply.
	Outcome(id int) (status, reply string)
	// AutoReport is the result of a run whose agent reported none.
	AutoReport(model.Thread) *Report
	Deliver(notification.Notice)
	// SyncLoginItem keeps the service starting at login while an
	// automation or the shared webhook is on.
	SyncLoginItem(enabled bool)
	// Changed tells windows the automations changed.
	Changed()
	// AddWorkspace lists a run's worktree as a project while it exists,
	// without trusting it in an agent's configuration; RemoveWorkspace
	// takes it and its conversations off the list.
	AddWorkspace(path string)
	RemoveWorkspace(path string)
}

// Runner runs the automations of the controller that owns them: the
// background service's. Windows reach it through the service. All its
// methods run on the host's thread.
type Runner struct {
	Path   string
	Store  Store
	Active map[string]*ActiveRun
	// Now, Submit, Check and Background are replaced in tests.
	Now    func() time.Time
	Submit func(model.SendRequest) int
	Check  func(dir, command string) (bool, string)
	// Background runs work off the host's thread, then its result on it.
	Background func(work func() func())
	// WorktreeRoot is where runs of Worktree automations get theirs.
	WorktreeRoot string
	// Probe probes a condition off the host's thread; replaced in tests.
	Probe func(c Condition, dir string) ProbeResult
	// Stream keeps a line trigger's command running until ctx ends and
	// reports what it does; replaced in tests.
	Stream func(ctx context.Context, dir string, c Condition, emit func(StreamEvent))
	// Try runs an automation's conditions once before it is saved; replaced
	// in tests.
	Try  func(a Automation) (map[string]Trial, error)
	host Host
	wake *wakeState
	// hook is the local HTTP wake endpoint; listen lets a scheduling
	// runner open it.
	hook        *webhook
	listen      bool
	relay       *relay
	relayStates map[string]relayStatus
}

// ActiveRun is a run in progress: its conversation and round.
type ActiveRun struct {
	Thread   int
	round    int
	checking bool
	// A manual run may start with its schedule paused. Keep that run
	// independent of the schedule without turning future scheduled runs on.
	allowPaused bool
	// started identifies the run in the store: runs skipped meanwhile
	// come after it.
	started time.Time
	// preparing marks a run whose worktree is being made; dir is where
	// the run works, "" for the automation's project.
	preparing bool
	dir       string
	// ev is what woke the run.
	ev WakeEvent
}

// TickInterval is how often the scheduler looks for due automations and
// rounds that ended.
const TickInterval = 15 * time.Second

// NewRunner loads the automations stored at path.
func NewRunner(host Host, path string) *Runner {
	rn := &Runner{Path: path, Store: Load(path), Active: map[string]*ActiveRun{},
		Now: time.Now, Submit: host.Submit, Check: runCheck, WorktreeRoot: WorktreeRoot(), Probe: runProbe, Stream: runStream, Try: TryConditions, host: host}
	rn.Background = func(work func() func()) {
		go func() {
			done := work()
			host.Dispatch(done)
		}()
	}
	return rn
}

// Start ends the runs that were in progress when the service stopped and,
// with schedule, checks the automations every TickInterval until done.
func (rn *Runner) Start(done <-chan struct{}, schedule bool) {
	// A run in progress when the service stopped ended with it.
	for _, a := range rn.Store.Automations {
		if r, ok := rn.Store.LastRun(a.ID); ok && r.Result == Running {
			rn.Store.UpdateLastRun(a.ID, func(r *Run) {
				r.Result, r.Summary, r.Finished = Interrupted, "Still running when Miya exited", rn.Now()
			})
		}
		// A worktree left by a run that ended with the service, or before
		// it was settled, is settled now.
		for _, r := range rn.Store.Runs[a.ID] {
			if r.Worktree != nil && (r.WorktreeState == WorktreeActive || r.WorktreeState == WorktreeBusy || r.WorktreeState == "") {
				rn.Store.UpdateRun(a.ID, r.Started, func(x *Run) { x.WorktreeState = WorktreeActive })
				rn.settle(a.ID, r.Started, false)
			}
		}
	}
	rn.Persist()
	rn.listen = schedule
	rn.host.SyncLoginItem(rn.Enabled())
	if !schedule {
		return
	}
	rn.syncWebhook()
	rn.syncRelay()
	relayWorker := rn.relay
	go func() {
		rn.host.Dispatch(rn.Tick)
		t := time.NewTicker(TickInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				// Dispatch may already ignore callbacks once the controller
				// closes. Cancel network work independently of that callback.
				if relayWorker != nil {
					relayWorker.cancel()
				}
				rn.host.Dispatch(func() {
					rn.listen = false
				})
				rn.host.Dispatch(rn.stopWebhook)
				rn.host.Dispatch(rn.stopStreams)
				return
			case <-t.C:
				rn.host.Dispatch(rn.Tick)
			}
		}
	}()
}

// Persist saves the automations, logging a failure.
func (rn *Runner) Persist() {
	if err := rn.Store.Save(rn.Path); err != nil {
		log.Printf("miya: saving automations: %v", err)
	}
}

// Enabled reports whether automations or the shared webhook need the
// service to stay running. A nil runner needs none.
func (rn *Runner) Enabled() bool {
	return rn != nil && (rn.Store.AnyEnabled() || rn.listen && !rn.Store.WebhookDisabled)
}

// due is the latest scheduled time of a that has come and has no run yet,
// zero when none has. Times missed while asleep make one run.
func due(a Automation, now time.Time) time.Time {
	base := a.Due
	if base.IsZero() {
		base = a.Created
	}
	next := a.Next(base)
	if next.IsZero() || next.After(now) {
		return time.Time{}
	}
	for i := 0; i < 100000; i++ {
		n := a.Next(next)
		if n.IsZero() || n.After(now) {
			break
		}
		next = n
	}
	return next
}

// Tick follows the runs in progress and starts the automations that are due.
func (rn *Runner) Tick() {
	if rn == nil {
		return
	}
	for id, r := range rn.Active {
		rn.advance(id, r)
	}
	now := rn.Now()
	changed := false
	for i := range rn.Store.Automations {
		a := rn.Store.Automations[i]
		if !a.Enabled {
			continue
		}
		d := due(a, now)
		if d.IsZero() {
			continue
		}
		rn.Store.Automations[i].Due = d
		changed = true
		_, _ = rn.deliver(rn.Store.Automations[i], WakeEvent{Automation: a.ID, Source: SourceSchedule, At: now,
			CatchUp: now.Sub(d) > 2*time.Minute, Reason: "Scheduled for " + FormatTime(d)}, false)
	}
	if changed {
		rn.Persist()
	}
	rn.tickWakes(now)
}

// StartRun starts a run now: a new conversation in the automation's
// project, sent its instructions.
func (rn *Runner) StartRun(id string, catchUp bool) error {
	return rn.startRun(id, WakeEvent{Automation: id, CatchUp: catchUp})
}

// startRun starts a run for what woke it.
func (rn *Runner) startRun(id string, ev WakeEvent) error {
	catchUp := ev.CatchUp
	i, ok := rn.Store.Find(id)
	if !ok {
		return errors.New("Automation not found")
	}
	a := rn.Store.Automations[i]
	if _, running := rn.Active[id]; running {
		return errors.New("The previous run has not finished")
	}
	now := rn.Now()
	var prev *Run
	if last, ok := rn.Store.LastReport(id); ok && !a.NoCarryOver {
		prev = &last
	}
	run := Run{Started: now, Round: 1, Rounds: a.Rounds, Result: Running, CatchUp: catchUp,
		WakeSource: ev.Source, WakeTrigger: ev.Trigger, WakeReason: ev.Reason}
	thread := 0
	if info, err := os.Stat(a.Project); err != nil || !info.IsDir() {
		run.Result, run.Summary, run.Finished = Failed, "Project directory does not exist: "+a.Project, now
	} else if a.Worktree {
		rn.startInWorktree(a, run, prev, ev)
		return nil
	} else if thread = rn.Submit(model.SendRequest{Agent: a.Agent, Project: a.Project, Text: RunMessage(a, prev, ev), MemoryControl: true}); thread == 0 {
		run.Result, run.Summary, run.Finished = Failed, model.AgentName(a.Agent)+" is not ready", now
	}
	rn.Store.AddRun(id, run)
	if thread != 0 {
		r := &ActiveRun{round: 1, started: now, allowPaused: !a.Enabled, ev: ev}
		rn.Active[id] = r
		rn.attach(id, a, r, thread)
	}
	rn.Persist()
	if run.Result == Failed {
		rn.notice(a, run)
		return errors.New(run.Summary)
	}
	return nil
}

// attach makes thread the conversation of a's run in progress, r.
func (rn *Runner) attach(id string, a Automation, r *ActiveRun, thread int) {
	title := a.Name + " · " + r.started.Format("Jan 2, 15:04")
	rn.host.UpdateThread(thread, func(t *model.Thread) {
		t.Title = title
		t.Automation = a.ID
	})
	r.Thread = thread
	serverID := rn.host.Thread(thread).ServerID
	rn.Store.UpdateRun(id, r.started, func(run *Run) { run.Thread, run.ServerID = thread, serverID })
}

// updateRun changes the run r follows, or the latest without one.
func (rn *Runner) updateRun(id string, r *ActiveRun, change func(*Run)) {
	if r == nil || !rn.Store.UpdateRun(id, r.started, change) {
		rn.Store.UpdateLastRun(id, change)
	}
}

// advance follows a run: once its round has ended, it checks the work and
// finishes the run or starts the next round.
func (rn *Runner) advance(id string, r *ActiveRun) {
	if r.checking || r.preparing {
		return
	}
	i, ok := rn.Store.Find(id)
	if !ok {
		delete(rn.Active, id)
		return
	}
	t := rn.host.Thread(r.Thread)
	if t.ID == 0 {
		rn.finish(id, Interrupted, "Conversation closed")
		return
	}
	if sid := t.ServerID; sid != "" {
		rn.updateRun(id, r, func(run *Run) { run.ServerID = sid })
	}
	// A round waiting for an approval or an answer has not ended.
	if t.Busy || t.Loading || len(t.Queue) > 0 || rn.host.AwaitsUser(t) {
		return
	}
	a := rn.Store.Automations[i]
	status, reply := rn.host.Outcome(t.ID)
	switch {
	case t.StopRequested || t.StoppedByUser():
		rn.finish(id, Interrupted, "Stopped in conversation")
	case !a.Enabled && !r.allowPaused:
		rn.finish(id, Interrupted, "Automation paused")
	case status == subagent.Failed:
		rn.finish(id, Failed, reply)
	case status != subagent.Completed:
		rn.finish(id, Failed, textutil.FirstNonEmpty(t.Status, "No response received"))
	case a.Check == "":
		rn.finish(id, Passed, reply)
	default:
		r.checking = true
		dir, command, check := textutil.FirstNonEmpty(r.dir, a.Project), a.Check, rn.Check
		rn.Background(func() func() {
			passed, output := check(dir, command)
			return func() { rn.afterCheck(id, r, passed, output, reply) }
		})
	}
}

func (rn *Runner) afterCheck(id string, r *ActiveRun, passed bool, output, reply string) {
	r.checking = false
	if rn.Active[id] != r {
		return
	}
	i, ok := rn.Store.Find(id)
	if !ok {
		delete(rn.Active, id)
		return
	}
	a := rn.Store.Automations[i]
	switch {
	case passed:
		rn.finish(id, Passed, reply)
		return
	case !a.Enabled && !r.allowPaused:
		rn.finish(id, Interrupted, "Automation paused")
		return
	case r.round >= a.Rounds:
		rn.finish(id, Failed, "Check failed: "+textutil.LastLine(output))
		return
	}
	r.round++
	rn.updateRun(id, r, func(run *Run) { run.Round = r.round })
	rn.Persist()
	if rn.Submit(model.SendRequest{ThreadID: r.Thread, Text: checkMessage(a, r.round-1, output), MemoryControl: true}) == 0 {
		rn.finish(id, Failed, "Unable to start the next round")
	}
}

// checkMessage asks for the next round, with what the check said.
func checkMessage(a Automation, round int, output string) string {
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return fmt.Sprintf("Check failed (round %d/%d). Output from `%s` follows. Fix the issue, then finish this round.\n\n```\n%s\n```", round, a.Rounds, a.Check, output)
}

func (rn *Runner) finish(id, result, summary string) {
	r := rn.Active[id]
	delete(rn.Active, id)
	now := rn.Now()
	var run Run
	rn.updateRun(id, r, func(x *Run) {
		x.Result, x.Summary, x.Finished = result, textutil.ClipRunes(textutil.FirstLine(summary), 200), now
		if r != nil {
			x.Round = r.round
		}
		if x.Report == nil && x.Thread != 0 {
			x.Report = rn.host.AutoReport(rn.host.Thread(x.Thread))
		}
		if result == Passed && x.Report != nil && !x.Report.Auto {
			x.Summary = textutil.ClipRunes(textutil.FirstLine(x.Report.Summary), 200)
		}
		run = *x
	})
	rn.Persist()
	rn.reprobe(id, run)
	if i, ok := rn.Store.Find(id); ok {
		defer rn.afterRun(rn.Store.Automations[i], run)
	}
	if run.Worktree != nil {
		// The notification says what the run left in its worktree.
		rn.settle(id, run.Started, true)
		return
	}
	if i, ok := rn.Store.Find(id); ok {
		rn.notice(rn.Store.Automations[i], run)
	}
}

// notice tells the phone how a run ended, once per run; the conversation's
// own completion notices stay quiet for automations.
func (rn *Runner) notice(a Automation, run Run) {
	title := a.Name + " · " + ResultLabel(run.Result)
	if run.Rounds > 1 {
		title += fmt.Sprintf("(round %d/%d)", run.Round, run.Rounds)
	}
	rn.host.Deliver(notification.Notice{ID: fmt.Sprintf("miya-auto-%s-%d", a.ID, run.Started.Unix()), Thread: strconv.Itoa(run.Thread),
		Title: title, Body: noticeBody(a, run)})
}

// noticeBody is the run's summary and, for a worktree run, where its
// changes are.
func noticeBody(a Automation, run Run) string {
	body := textutil.FirstNonEmpty(run.Summary, a.Name)
	if label := WorktreeLabel(run); label != "" {
		body += "\n" + label
	}
	return body
}

// runCheck runs a check command in a login shell, as the person would in a
// terminal, for at most ten minutes. It returns whether it passed and the
// end of what it printed.
func runCheck(dir, command string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, textutil.FirstNonEmpty(os.Getenv("SHELL"), "/bin/sh"), "-lc", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := string(out)
	if ctx.Err() == context.DeadlineExceeded {
		text += "\n(Check stopped after exceeding 10 minutes)"
	}
	if r := []rune(text); len(r) > 4000 {
		text = "…" + string(r[len(r)-4000:])
	}
	return err == nil, strings.TrimSpace(text)
}
