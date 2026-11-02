package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"miya/internal/codex"
)

// automationState is the automations of the controller that runs them: the
// background service's. Windows reach it through the service.
type automationState struct {
	path   string
	store  automationStore
	active map[string]*activeAutomationRun
	// now, submit, check and background are replaced in tests.
	now    func() time.Time
	submit func(sendRequest) int
	check  func(dir, command string) (bool, string)
	// background runs work off the main thread, then its result on it.
	background func(work func() func())
}

// activeAutomationRun is a run in progress: its conversation and round.
type activeAutomationRun struct {
	thread   int
	round    int
	checking bool
}

// automationTick is how often the scheduler looks for due automations and
// rounds that ended.
const automationTick = 15 * time.Second

// startAutomations loads the automations and, with schedule, checks them
// every automationTick from now on.
func (c *chatController) startAutomations(path string, schedule bool) {
	s := &automationState{path: path, store: loadAutomations(path), active: map[string]*activeAutomationRun{},
		now: time.Now, submit: c.submit, check: runAutomationCheck}
	s.background = func(work func() func()) {
		go func() {
			done := work()
			c.dispatch(done)
		}()
	}
	c.auto = s
	// A run in progress when the service stopped ended with it.
	for _, a := range s.store.Automations {
		if r, ok := s.store.lastRun(a.ID); ok && r.Result == runRunning {
			s.store.updateLastRun(a.ID, func(r *AutomationRun) {
				r.Result, r.Summary, r.Finished = runInterrupted, "Miya 退出时仍在运行", s.now()
			})
		}
	}
	s.persist()
	syncAutomationLaunchAgent(s.store.anyEnabled())
	if !schedule {
		return
	}
	done := make(<-chan struct{})
	if c.ctx != nil {
		done = c.ctx.Done()
	}
	go func() {
		c.dispatch(c.automationTick)
		t := time.NewTicker(automationTick)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				c.dispatch(c.automationTick)
			}
		}
	}()
}

func (s *automationState) persist() {
	if err := s.store.save(s.path); err != nil {
		log.Printf("miya: saving automations: %v", err)
	}
}

// automationsEnabled reports whether some automation is on: the service
// stays up for it.
func (c *chatController) automationsEnabled() bool {
	return c.auto != nil && c.auto.store.anyEnabled()
}

// automationDue is the latest scheduled time of a that has come and has no
// run yet, zero when none has. Times missed while asleep make one run.
func automationDue(a Automation, now time.Time) time.Time {
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

func (c *chatController) automationTick() {
	s := c.auto
	if s == nil {
		return
	}
	for id, r := range s.active {
		c.advanceAutomation(id, r)
	}
	now := s.now()
	changed := false
	for i := range s.store.Automations {
		a := s.store.Automations[i]
		if !a.Enabled {
			continue
		}
		due := automationDue(a, now)
		if due.IsZero() {
			continue
		}
		s.store.Automations[i].Due = due
		changed = true
		if _, running := s.active[a.ID]; running {
			s.store.addRun(a.ID, AutomationRun{Started: now, Finished: now, Result: runSkipped, Summary: "上一次运行还没结束"})
			continue
		}
		_ = c.startAutomationRun(a.ID, now.Sub(due) > 2*time.Minute)
	}
	if changed {
		s.persist()
	}
}

// startAutomationRun starts a run now: a new conversation in the
// automation's project, sent its instructions.
func (c *chatController) startAutomationRun(id string, catchUp bool) error {
	s := c.auto
	i, ok := s.store.find(id)
	if !ok {
		return errors.New("没有这个自动化")
	}
	a := s.store.Automations[i]
	if _, running := s.active[id]; running {
		return errors.New("上一次运行还没结束")
	}
	now := s.now()
	var prev *AutomationRun
	if last, ok := s.store.lastReport(id); ok && !a.NoCarryOver {
		prev = &last
	}
	run := AutomationRun{Started: now, Round: 1, Rounds: a.Rounds, Result: runRunning, CatchUp: catchUp}
	thread := 0
	if info, err := os.Stat(a.Project); err != nil || !info.IsDir() {
		run.Result, run.Summary, run.Finished = runFailed, "项目目录不存在："+a.Project, now
	} else if thread = s.submit(sendRequest{Agent: a.Agent, Project: a.Project, Text: automationRunMessage(a, prev), MemoryControl: true}); thread == 0 {
		run.Result, run.Summary, run.Finished = runFailed, backendFor(a.Agent).Name()+" 还没有就绪", now
	}
	if thread != 0 {
		title := a.Name + " · " + now.Format("1月2日 15:04")
		c.update(thread, func(t *Thread) {
			t.Title = title
			t.Automation = a.ID
		})
		run.Thread, run.ServerID = thread, c.threadWithID(thread).ServerID
		s.active[id] = &activeAutomationRun{thread: thread, round: 1}
	}
	s.store.addRun(id, run)
	s.persist()
	if run.Result == runFailed {
		c.automationNotice(a, run)
		return errors.New(run.Summary)
	}
	return nil
}

// advanceAutomation follows a run: once its round has ended, it checks the
// work and finishes the run or starts the next round.
func (c *chatController) advanceAutomation(id string, r *activeAutomationRun) {
	s := c.auto
	if r.checking {
		return
	}
	i, ok := s.store.find(id)
	if !ok {
		delete(s.active, id)
		return
	}
	t := c.threadWithID(r.thread)
	if t.ID == 0 {
		c.finishAutomationRun(id, runInterrupted, "对话已关闭")
		return
	}
	if sid := t.ServerID; sid != "" {
		s.store.updateLastRun(id, func(run *AutomationRun) { run.ServerID = sid })
	}
	// A round waiting for an approval or an answer has not ended.
	if t.Busy || t.Loading || len(t.Queue) > 0 || c.threadAwaitsUser(t) {
		return
	}
	a := s.store.Automations[i]
	status, reply := c.handoffState(t.ID)
	switch {
	case t.StopRequested || t.Status == "Stopped":
		c.finishAutomationRun(id, runInterrupted, "在对话中停止")
	case !a.Enabled:
		c.finishAutomationRun(id, runInterrupted, "自动化已暂停")
	case status == subAgentFailed:
		c.finishAutomationRun(id, runFailed, reply)
	case status != subAgentCompleted:
		c.finishAutomationRun(id, runFailed, firstNonEmpty(t.Status, "没有收到回复"))
	case a.Check == "":
		c.finishAutomationRun(id, runPassed, reply)
	default:
		r.checking = true
		dir, command, check := a.Project, a.Check, s.check
		s.background(func() func() {
			passed, output := check(dir, command)
			return func() { c.afterAutomationCheck(id, r, passed, output, reply) }
		})
	}
}

func (c *chatController) afterAutomationCheck(id string, r *activeAutomationRun, passed bool, output, reply string) {
	s := c.auto
	r.checking = false
	if s.active[id] != r {
		return
	}
	i, ok := s.store.find(id)
	if !ok {
		delete(s.active, id)
		return
	}
	a := s.store.Automations[i]
	switch {
	case passed:
		c.finishAutomationRun(id, runPassed, reply)
		return
	case !a.Enabled:
		c.finishAutomationRun(id, runInterrupted, "自动化已暂停")
		return
	case r.round >= a.Rounds:
		c.finishAutomationRun(id, runFailed, "检查未通过："+lastLine(output))
		return
	}
	r.round++
	s.store.updateLastRun(id, func(run *AutomationRun) { run.Round = r.round })
	s.persist()
	if s.submit(sendRequest{ThreadID: r.thread, Text: automationCheckMessage(a, r.round-1, output), MemoryControl: true}) == 0 {
		c.finishAutomationRun(id, runFailed, "无法开始下一轮")
	}
}

// automationCheckMessage asks for the next round, with what the check said.
func automationCheckMessage(a Automation, round int, output string) string {
	if strings.TrimSpace(output) == "" {
		output = "（没有输出）"
	}
	return fmt.Sprintf("检查未通过（第 %d/%d 轮）：`%s` 的输出如下，请修复后结束这一轮。\n\n```\n%s\n```", round, a.Rounds, a.Check, output)
}

func (c *chatController) finishAutomationRun(id, result, summary string) {
	s := c.auto
	r := s.active[id]
	delete(s.active, id)
	now := s.now()
	var run AutomationRun
	s.store.updateLastRun(id, func(x *AutomationRun) {
		x.Result, x.Summary, x.Finished = result, clipRunes(firstLine(summary), 200), now
		if r != nil {
			x.Round = r.round
		}
		if x.Report == nil && x.Thread != 0 {
			x.Report = autoRunReport(c.threadWithID(x.Thread))
		}
		if result == runPassed && x.Report != nil && !x.Report.Auto {
			x.Summary = clipRunes(firstLine(x.Report.Summary), 200)
		}
		run = *x
	})
	s.persist()
	if i, ok := s.store.find(id); ok {
		c.automationNotice(s.store.Automations[i], run)
	}
}

// automationNotice tells the phone how a run ended, once per run; the
// conversation's own completion notices stay quiet for automations.
func (c *chatController) automationNotice(a Automation, run AutomationRun) {
	title := a.Name + " · " + runResultLabel(run.Result)
	if run.Rounds > 1 {
		title += fmt.Sprintf("（第 %d/%d 轮）", run.Round, run.Rounds)
	}
	c.deliverNotice(taskNotice{ID: fmt.Sprintf("miya-auto-%s-%d", a.ID, run.Started.Unix()), Thread: strconv.Itoa(run.Thread),
		Title: title, Body: firstNonEmpty(run.Summary, a.Name)}, "completion")
}

// approvalThread is the conversation an approval waits in, Codex's by its
// thread id and Claude Code's by the conversation in its request id.
func (c *chatController) approvalThread(a codex.Approval) (Thread, bool) {
	if claudeApproval(a) {
		var s string
		_ = json.Unmarshal(a.ID, &s)
		var id int
		if _, err := fmt.Sscanf(s, "claude:%d:", &id); err != nil {
			return Thread{}, false
		}
		t := c.threadWithID(id)
		return t, t.ID != 0
	}
	if a.ThreadID == "" {
		return Thread{}, false
	}
	for _, t := range c.threads() {
		if t.ServerID == a.ThreadID {
			return t, true
		}
	}
	for path, threads := range c.projectCache {
		if path == c.cwd {
			continue
		}
		for _, t := range threads {
			if t.ServerID == a.ThreadID {
				return t, true
			}
		}
	}
	return Thread{}, false
}

// threadAwaitsUser reports whether a conversation waits for an approval or
// an answer.
func (c *chatController) threadAwaitsUser(t Thread) bool {
	if c.approvals == nil {
		return false
	}
	for _, a := range c.approvals() {
		if at, ok := c.approvalThread(a); ok && at.ID == t.ID {
			return true
		}
	}
	return false
}

// runAutomationCheck runs a check command in a login shell, as the person
// would in a terminal, for at most ten minutes. It returns whether it
// passed and the end of what it printed.
func runAutomationCheck(dir, command string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, firstNonEmpty(os.Getenv("SHELL"), "/bin/sh"), "-lc", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := string(out)
	if ctx.Err() == context.DeadlineExceeded {
		text += "\n（检查超过 10 分钟，已停止）"
	}
	if r := []rune(text); len(r) > 4000 {
		text = "…" + string(r[len(r)-4000:])
	}
	return err == nil, strings.TrimSpace(text)
}

// lastLine is the last line of s that says something.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
