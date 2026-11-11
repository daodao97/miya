package automation

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"miya/internal/notification"
	"miya/internal/textutil"
)

// WakeGathered is the outcome of an event a debounce is gathering with
// others: it is recorded once they are delivered together.
const WakeGathered = "gathered"

// sleepGap is how far the wall clock may move between two ticks before
// the computer counts as having slept: the ticker stops while it sleeps.
const sleepGap = 2 * time.Minute

// probeErrorNotice is how many failed probes in a row tell the person.
const probeErrorNotice = 3

// pendingWake is a debounce gathering the events of a trigger.
type pendingWake struct {
	ev       WakeEvent
	deadline time.Time
	data     []string
}

// wakeState is what the runner keeps about wakes while it runs.
type wakeState struct {
	probing  map[string]bool
	scanning map[string]bool
	gating   map[string]bool
	pending  map[string]*pendingWake
	queued   map[string]WakeEvent
	files    map[string]fileSnapshot
	streams  map[string]*streamProc
	lastTick time.Time
}

func (rn *Runner) wakes() *wakeState {
	if rn.wake == nil {
		rn.wake = &wakeState{probing: map[string]bool{}, scanning: map[string]bool{}, gating: map[string]bool{},
			pending: map[string]*pendingWake{}, queued: map[string]WakeEvent{}, files: map[string]fileSnapshot{}, streams: map[string]*streamProc{}}
	}
	return rn.wake
}

// triggered reports whether a source is one of the triggers, which the
// policy holds back; schedules and Run now are not held back.
func triggered(source string) bool {
	return source != SourceSchedule && source != SourceManual && source != ""
}

func clipData(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > DataLimit {
		s = string(r[:DataLimit]) + "…"
	}
	return s
}

// Wake takes an event through the pipeline: it is matched to its
// automation and trigger, deduplicated, gathered by a debounce, held back
// by the policy, and, when it gets through, starts a run. What became of
// it is recorded with the automation. An error says why it started
// nothing when whoever woke the automation should know.
func (rn *Runner) Wake(ev WakeEvent) (Wake, error) {
	w := rn.wakes()
	if ev.At.IsZero() {
		ev.At = rn.Now()
	}
	ev.Data, ev.Reason = clipData(ev.Data), textutil.ClipRunes(strings.TrimSpace(ev.Reason), 300)
	i, ok := rn.Store.Find(ev.Automation)
	if !ok {
		return Wake{}, errors.New("Automation not found")
	}
	a := rn.Store.Automations[i]
	var t Trigger
	if ev.Trigger != "" {
		if t, ok = a.Trigger(ev.Trigger); !ok {
			return Wake{}, errors.New("Trigger not found")
		}
		if t.Disabled {
			return rn.record(a.ID, ev, WakeDropped, "The trigger is off"), errors.New("The trigger is off")
		}
	}
	if !a.Enabled && ev.Source != SourceManual {
		return rn.record(a.ID, ev, WakeDropped, "The automation is paused"), errors.New("The automation is paused")
	}
	if ev.Dedupe != "" {
		for _, old := range rn.Store.Wakes[a.ID] {
			if old.Dedupe == ev.Dedupe {
				return Wake{At: ev.At, Source: ev.Source, Trigger: ev.Trigger, Outcome: WakeDropped, Detail: "Already delivered"}, nil
			}
		}
	}
	if window := debounceWindow(a, t, ev.Source); window > 0 {
		key := TriggerKey(a.ID, ev.Trigger)
		if p := w.pending[key]; p != nil {
			p.ev.Count++
			if ev.Data != "" {
				p.data = append(p.data, ev.Data)
			}
		} else {
			p = &pendingWake{ev: ev, deadline: ev.At.Add(window)}
			p.ev.Count = 1
			if ev.Data != "" {
				p.data = []string{ev.Data}
			}
			w.pending[key] = p
		}
		return Wake{At: ev.At, Source: ev.Source, Trigger: ev.Trigger, Reason: ev.Reason, Outcome: WakeGathered}, nil
	}
	return rn.deliver(a, ev, false)
}

// debounceWindow is how long a trigger's events are gathered into one.
func debounceWindow(a Automation, t Trigger, source string) time.Duration {
	// The lines a running command prints are events like the others; a
	// command that is checked now and then wakes at most once a check.
	line := source == TriggerCondition && t.Condition != nil && t.Condition.When == WhenLine
	switch source {
	case TriggerIngress, TriggerFile, TriggerAfter, TriggerSystem:
	default:
		if !line {
			return 0
		}
	}
	if a.Policy.DebounceSeconds > 0 {
		return time.Duration(a.Policy.DebounceSeconds) * time.Second
	}
	if line {
		return defaultLineDebounce * time.Second
	}
	if t.Kind == TriggerFile {
		return defaultFileDebounce * time.Second
	}
	return 0
}

// deliver applies the policy to an event and starts its run; gated marks
// an event whose gate has been probed and met.
func (rn *Runner) deliver(a Automation, ev WakeEvent, gated bool) (Wake, error) {
	w := rn.wakes()
	now := rn.Now()
	if triggered(ev.Source) {
		if cd := time.Duration(a.Policy.CooldownMinutes) * time.Minute; cd > 0 {
			if last := rn.lastTriggeredRun(a.ID); !last.IsZero() && now.Sub(last) < cd {
				return rn.record(a.ID, ev, WakeHeld, "Cooling down until "+FormatTime(last.Add(cd))), nil
			}
		}
		if n := rn.triggeredRuns(a.ID, now.Add(-24*time.Hour)); n >= a.Policy.MaxPerDay {
			return rn.record(a.ID, ev, WakeHeld, fmt.Sprintf("Reached %d triggered runs in 24 hours", n)), nil
		}
	}
	_, running := rn.Active[a.ID]
	if running || w.gating[a.ID] {
		if a.Policy.Queue && ev.Source != SourceManual {
			if q, waiting := w.queued[a.ID]; waiting {
				q.Count = max(q.Count, 1) + 1
				if ev.Data != "" {
					q.Data = clipData(q.Data + "\n\n" + ev.Data)
				}
				w.queued[a.ID] = q
				return rn.record(a.ID, ev, WakeSkipped, "Merged into the wake already waiting"), nil
			}
			w.queued[a.ID] = ev
			return rn.record(a.ID, ev, WakeQueued, "Waiting for the run in progress"), nil
		}
		if ev.Source == SourceSchedule {
			rn.Store.AddRun(a.ID, Run{Started: now, Finished: now, Result: Skipped, Summary: "The previous run has not finished"})
		}
		wk := rn.record(a.ID, ev, WakeSkipped, "The previous run has not finished")
		if ev.Source == SourceManual || ev.Source == TriggerIngress {
			return wk, errors.New("The previous run has not finished")
		}
		return wk, nil
	}
	if g := a.Policy.Gate; g != nil && !gated && ev.Source != SourceManual {
		w.gating[a.ID] = true
		gate, dir, probe := *g, a.Project, rn.Probe
		rn.Background(func() func() {
			res := probe(gate, dir)
			return func() { rn.afterGate(a.ID, ev, gate, res) }
		})
		return Wake{At: ev.At, Source: ev.Source, Trigger: ev.Trigger, Reason: ev.Reason, Outcome: WakeGathered, Detail: "Checking the gate"}, nil
	}
	started := rn.Now()
	if err := rn.startRun(a.ID, ev); err != nil {
		return rn.record(a.ID, ev, WakeFailed, err.Error()), err
	}
	wk := Wake{At: ev.At, Source: ev.Source, Trigger: ev.Trigger, Reason: ev.Reason, Dedupe: ev.Dedupe, Outcome: WakeStarted, Started: started}
	rn.Store.AddWake(a.ID, wk)
	rn.Persist()
	return wk, nil
}

func (rn *Runner) afterGate(id string, ev WakeEvent, gate Condition, res ProbeResult) {
	delete(rn.wakes().gating, id)
	i, ok := rn.Store.Find(id)
	if !ok {
		return
	}
	a := rn.Store.Automations[i]
	switch {
	case res.Err != "":
		rn.record(id, ev, WakeHeld, "Gate check failed: "+res.Err)
	case !res.Met:
		rn.record(id, ev, WakeHeld, "Gate not met: "+textutil.FirstNonEmpty(res.Value, DescribeTrigger(Trigger{Kind: TriggerCondition, Condition: &gate}, nil)))
	default:
		if ev.Reason == "" {
			ev.Reason = res.Value
		} else if res.Value != "" {
			ev.Reason += "; " + res.Value
		}
		_, _ = rn.deliver(a, ev, true)
	}
}

// record keeps what became of an event that started no run.
func (rn *Runner) record(id string, ev WakeEvent, outcome, detail string) Wake {
	w := Wake{At: ev.At, Source: ev.Source, Trigger: ev.Trigger, Reason: ev.Reason, Dedupe: ev.Dedupe, Outcome: outcome, Detail: detail}
	if w.At.IsZero() {
		w.At = rn.Now()
	}
	rn.Store.AddWake(id, w)
	rn.Persist()
	rn.host.Changed()
	return w
}

// lastTriggeredRun is when a trigger last started a run of an automation.
func (rn *Runner) lastTriggeredRun(id string) time.Time {
	wakes := rn.Store.Wakes[id]
	for i := len(wakes) - 1; i >= 0; i-- {
		if wakes[i].Outcome == WakeStarted && triggered(wakes[i].Source) {
			return wakes[i].Started
		}
	}
	return time.Time{}
}

// triggeredRuns counts the runs triggers started since.
func (rn *Runner) triggeredRuns(id string, since time.Time) int {
	n := 0
	for _, w := range rn.Store.Wakes[id] {
		if w.Outcome == WakeStarted && triggered(w.Source) && w.Started.After(since) {
			n++
		}
	}
	return n
}

// tickWakes follows the triggers: it delivers gathered and queued events,
// notices the computer waking, probes conditions and scans watched files.
func (rn *Runner) tickWakes(now time.Time) {
	w := rn.wakes()
	if !w.lastTick.IsZero() && now.Round(0).Sub(w.lastTick.Round(0)) > sleepGap {
		rn.systemEvent(SystemWake, fmt.Sprintf("The computer woke after %s", now.Round(0).Sub(w.lastTick.Round(0)).Round(time.Minute)))
	}
	w.lastTick = now
	for key, p := range w.pending {
		if now.Before(p.deadline) {
			continue
		}
		delete(w.pending, key)
		ev := p.ev
		if ev.Count > 1 {
			ev.Reason = fmt.Sprintf("%s (and %d more)", ev.Reason, ev.Count-1)
		}
		ev.Data = clipData(strings.Join(p.data, "\n\n"))
		if i, ok := rn.Store.Find(ev.Automation); ok {
			_, _ = rn.deliver(rn.Store.Automations[i], ev, false)
		}
	}
	for id, ev := range w.queued {
		if _, running := rn.Active[id]; running || w.gating[id] {
			continue
		}
		delete(w.queued, id)
		i, ok := rn.Store.Find(id)
		if !ok || !rn.Store.Automations[i].Enabled {
			continue
		}
		if ev.Count > 1 {
			ev.Reason = fmt.Sprintf("%s (and %d more while running)", ev.Reason, ev.Count-1)
		}
		_, _ = rn.deliver(rn.Store.Automations[i], ev, false)
	}
	for _, a := range rn.Store.Automations {
		if !a.Enabled {
			continue
		}
		for _, t := range a.Triggers {
			if t.Disabled {
				continue
			}
			switch t.Kind {
			case TriggerCondition:
				rn.probeIfDue(a, t, now)
			case TriggerFile:
				rn.scanFile(a, t)
			}
		}
	}
	rn.syncStreams()
}

// probeIfDue probes a condition when its interval has passed; not while a
// run is in progress, whose work may be changing what it measures.
func (rn *Runner) probeIfDue(a Automation, t Trigger, now time.Time) {
	w := rn.wakes()
	key := TriggerKey(a.ID, t.ID)
	if _, running := rn.Active[a.ID]; running || w.probing[key] || t.Condition == nil || t.Condition.When == WhenLine {
		return
	}
	st := rn.Store.Triggers[key]
	if !st.LastProbe.IsZero() && now.Sub(st.LastProbe) < time.Duration(t.Condition.EveryMinutes)*time.Minute {
		return
	}
	w.probing[key] = true
	c, dir, probe := *t.Condition, a.Project, rn.Probe
	rn.Background(func() func() {
		res := probe(c, dir)
		return func() {
			delete(rn.wakes().probing, key)
			rn.afterProbe(a.ID, t.ID, res)
		}
	})
}

// afterProbe updates a condition with what its probe found and wakes the
// automation when the condition turns met, or stays met past RepeatHours.
// A met condition rearms once it recovers.
func (rn *Runner) afterProbe(id, tid string, res ProbeResult) {
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
	if res.Err != "" {
		st.Errors++
		st.Error = textutil.ClipRunes(res.Err, 300)
		rn.Store.Triggers[key] = st
		rn.Persist()
		rn.host.Changed()
		if st.Errors == probeErrorNotice {
			rn.host.Deliver(notification.Notice{ID: "miya-auto-probe-" + key, Title: a.Name + " · " + tr("Condition check failing"), Body: st.Error})
		}
		return
	}
	st.Errors, st.Error, st.Met, st.Value = 0, "", res.Met, res.Value
	if t.Condition.When == WhenChanged {
		// Each change wakes it, with what the command printed as the run's
		// data. The first look only remembers what is there.
		fp := textFingerprint(res.Output)
		changed := st.Fingerprint != "" && st.Fingerprint != fp
		st.Fingerprint = fp
		if changed {
			st.LastFired = now
		}
		rn.Store.Triggers[key] = st
		rn.Persist()
		rn.host.Changed()
		if changed {
			_, _ = rn.Wake(WakeEvent{Automation: id, Trigger: tid, Source: TriggerCondition, Reason: conditionReason(*t.Condition, res), Data: res.Output})
		}
		return
	}
	fire, again := false, false
	if res.Met {
		if !st.Fired {
			fire = true
		} else if now.Sub(st.LastFired) >= time.Duration(t.Condition.RepeatHours)*time.Hour {
			fire, again = true, true
		}
	} else {
		st.Fired = false
	}
	if fire {
		st.Fired, st.LastFired = true, now
	}
	rn.Store.Triggers[key] = st
	rn.Persist()
	rn.host.Changed()
	if !fire {
		return
	}
	reason := conditionReason(*t.Condition, res)
	if again {
		reason = "Still met since the last run: " + reason
	}
	_, _ = rn.Wake(WakeEvent{Automation: id, Trigger: tid, Source: TriggerCondition, Reason: reason})
}

// conditionReason says why a met condition woke the automation.
func conditionReason(c Condition, res ProbeResult) string {
	what := "`" + commandLabel(c) + "`"
	switch {
	case c.When == WhenChanged:
		return fmt.Sprintf("The output of %s changed: %s", what, textutil.FirstNonEmpty(res.Value, "it is now empty"))
	case c.Text != "":
		// A recipe says in words what it checks; its command says what it found.
		return strings.TrimSuffix(conditionText(c, false)+": "+res.Value, ": ")
	case res.Value != "":
		return fmt.Sprintf("%s succeeded: %s", what, res.Value)
	}
	return what + " succeeded"
}

// scanFile looks at a watched path, at most one look at a time.
func (rn *Runner) scanFile(a Automation, t Trigger) {
	w := rn.wakes()
	key := TriggerKey(a.ID, t.ID)
	if w.scanning[key] {
		return
	}
	w.scanning[key] = true
	path := t.Path
	rn.Background(func() func() {
		snap, err := snapshotPath(path)
		return func() {
			delete(rn.wakes().scanning, key)
			rn.afterScan(a.ID, t.ID, snap, err)
		}
	})
}

// afterScan wakes the automation when a watched path changed since it was
// last seen. The first look only remembers what is there.
func (rn *Runner) afterScan(id, tid string, snap fileSnapshot, err error) {
	i, ok := rn.Store.Find(id)
	if !ok {
		return
	}
	t, ok := rn.Store.Automations[i].Trigger(tid)
	if !ok {
		return
	}
	w := rn.wakes()
	key := TriggerKey(id, tid)
	if rn.Store.Triggers == nil {
		rn.Store.Triggers = map[string]TriggerState{}
	}
	st := rn.Store.Triggers[key]
	if err != nil {
		if st.Error != err.Error() {
			st.Error = err.Error()
			rn.Store.Triggers[key] = st
			rn.Persist()
		}
		return
	}
	fp := snap.fingerprint()
	prev, seen := w.files[key]
	w.files[key] = snap
	if st.Fingerprint == fp && st.Error == "" {
		return
	}
	first := st.Fingerprint == ""
	changed := st.Fingerprint != fp
	st.Fingerprint, st.Error = fp, ""
	rn.Store.Triggers[key] = st
	rn.Persist()
	if first || !changed {
		return
	}
	reason, data := t.Path+" changed while Miya was not watching", ""
	if seen {
		added, removed, modified := fileChanges(prev, snap)
		reason = fmt.Sprintf("%s changed: %d added, %d removed, %d modified", t.Path, len(added), len(removed), len(modified))
		data = describeFileChanges(added, removed, modified)
	}
	_, _ = rn.Wake(WakeEvent{Automation: id, Trigger: tid, Source: TriggerFile, Reason: reason, Data: data})
}

// systemEvent wakes the automations with a system trigger for event.
func (rn *Runner) systemEvent(event, reason string) {
	for _, a := range rn.Store.Automations {
		if !a.Enabled {
			continue
		}
		for _, t := range a.Triggers {
			if t.Kind == TriggerSystem && !t.Disabled && t.Event == event {
				_, _ = rn.Wake(WakeEvent{Automation: a.ID, Trigger: t.ID, Source: TriggerSystem, Reason: reason})
			}
		}
	}
}

// afterRun wakes the automations that follow one whose run just ended.
func (rn *Runner) afterRun(a Automation, run Run) {
	if run.Result == Running || run.Result == Skipped {
		return
	}
	for _, b := range rn.Store.Automations {
		if !b.Enabled || b.ID == a.ID {
			continue
		}
		for _, t := range b.Triggers {
			if t.Kind != TriggerAfter || t.Disabled || t.After != a.ID {
				continue
			}
			if len(t.Results) > 0 && !slices.Contains(t.Results, run.Result) {
				continue
			}
			data := "Summary: " + run.Summary
			if run.Report != nil {
				data = FormatReport(*run.Report)
			}
			_, _ = rn.Wake(WakeEvent{Automation: b.ID, Trigger: t.ID, Source: TriggerAfter,
				Reason: fmt.Sprintf("\"%s\" finished: %s", a.Name, ResultLabel(run.Result)), Data: data,
				Dedupe: fmt.Sprintf("after:%s@%d", a.ID, run.Started.UnixNano())})
		}
	}
}

// reprobe probes the condition that woke a run once the run ended, to
// record whether the run helped.
func (rn *Runner) reprobe(id string, run Run) {
	if run.WakeSource != TriggerCondition || run.WakeTrigger == "" {
		return
	}
	i, ok := rn.Store.Find(id)
	if !ok {
		return
	}
	a := rn.Store.Automations[i]
	t, ok := a.Trigger(run.WakeTrigger)
	if !ok || t.Condition == nil || t.Condition.When == WhenLine {
		return
	}
	c, dir, probe, started := *t.Condition, textutil.FirstNonEmpty(a.Project), rn.Probe, run.Started
	rn.Background(func() func() {
		res := probe(c, dir)
		return func() {
			if res.Err == "" {
				rn.Store.UpdateRun(id, started, func(r *Run) { r.WakeAfter = res.Value })
			}
			rn.afterProbe(id, t.ID, res)
		}
	})
}

// Ingress wakes an automation from outside Miya through one of its ingress
// triggers. With a token, as an HTTP wake brings, it must be the trigger's;
// the service socket's caller is trusted and brings none.
func (rn *Runner) Ingress(ref, trigger, token string, checkToken bool, data, dedupe string) (Wake, error) {
	i, ok := rn.Store.Find(ref)
	if !ok {
		for j, a := range rn.Store.Automations {
			if strings.EqualFold(a.Name, strings.TrimSpace(ref)) {
				i, ok = j, true
				break
			}
		}
	}
	if !ok {
		return Wake{}, fmt.Errorf("Automation %q not found", ref)
	}
	a := rn.Store.Automations[i]
	for _, t := range a.Triggers {
		if t.Kind != TriggerIngress || trigger != "" && t.ID != trigger {
			continue
		}
		if checkToken && subtle.ConstantTimeCompare([]byte(t.Token), []byte(token)) != 1 {
			return Wake{}, errInvalidToken
		}
		reason := "Woken from outside Miya"
		if checkToken {
			reason = "Woken by an HTTP request"
		}
		return rn.Wake(WakeEvent{Automation: a.ID, Trigger: t.ID, Source: TriggerIngress, Reason: reason, Data: data, Dedupe: dedupe})
	}
	if checkToken {
		return Wake{}, errInvalidToken
	}
	return Wake{}, fmt.Errorf("\"%s\" has no external trigger. Add one, or use Run now", a.Name)
}

var errInvalidToken = errors.New("Invalid wake token")

// IsInvalidToken reports whether a wake was refused for its token.
func IsInvalidToken(err error) bool { return errors.Is(err, errInvalidToken) }

// IngressTokens lists the ingress triggers that HTTP wakes may reach.
func (s Store) HasIngress() bool {
	for _, a := range s.Automations {
		if a.Enabled && a.HasTrigger(TriggerIngress) {
			return true
		}
	}
	return false
}
