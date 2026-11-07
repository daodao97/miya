package automation

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"miya/internal/appdir"
	"miya/internal/chat/model"
	"miya/internal/project/worktree"
)

// A worktree run's WorktreeState is one of these.
const (
	// WorktreeActive: the run is working in it.
	WorktreeActive = "active"
	// WorktreeKept: the run changed something, waiting for merge or discard.
	WorktreeKept = "kept"
	// WorktreeCleaned: the run changed nothing, so it was removed.
	WorktreeCleaned = "cleaned"
	WorktreeMerged  = "merged"
	// WorktreeDiscarded: removed with its changes, or found gone.
	WorktreeDiscarded = "discarded"
	// WorktreeBusy: a merge or discard is in progress.
	WorktreeBusy = "busy"
)

// Worktree actions people take on a kept worktree.
const (
	WorktreeMerge   = "merge"
	WorktreeDiscard = "discard"
)

// WorktreeRoot is where runs' worktrees are made.
func WorktreeRoot() string { return filepath.Join(appdir.Dir(), "worktrees") }

func worktreeName(a Automation, started time.Time) string {
	label := worktree.Slug(a.Name)
	if label == "run" {
		label = "auto-" + a.ID
	}
	return label + "-" + started.Format("0102-1504")
}

// worktreeNote tells the agent where it works, so that it leaves the
// project's checkout alone and its changes where Miya can merge them.
func worktreeNote(w worktree.Worktree) string {
	return fmt.Sprintf("\n\nThis run works in a separate Git worktree (%s) on branch `%s`, started from the project's current commit; the project's own checkout is not affected. Make all changes here. You may commit them on this branch; do not merge, push or switch branches. When the run ends, Miya offers to merge the branch into the project or discard it.", w.Dir, w.Branch)
}

// startInWorktree starts run once its worktree exists. Making it runs
// off the host's thread; the run counts as in progress meanwhile.
func (rn *Runner) startInWorktree(a Automation, run Run, prev *Run) {
	r := &ActiveRun{round: 1, started: run.Started, preparing: true, allowPaused: !a.Enabled}
	rn.Active[a.ID] = r
	rn.Store.AddRun(a.ID, run)
	rn.Persist()
	project, root, name := a.Project, rn.WorktreeRoot, worktreeName(a, run.Started)
	rn.Background(func() func() {
		w, err := worktree.Create(context.Background(), project, root, "auto", name)
		return func() { rn.worktreeReady(a.ID, r, w, err, prev) }
	})
}

func (rn *Runner) worktreeReady(id string, r *ActiveRun, w worktree.Worktree, err error, prev *Run) {
	i, found := rn.Store.Find(id)
	if rn.Active[id] != r || !found {
		// Deleted or ended while it was being made: nothing will use it.
		if err == nil {
			rn.Background(func() func() {
				_ = w.Remove(context.Background(), false)
				return func() {}
			})
		}
		return
	}
	if err != nil {
		rn.finish(id, Failed, "Unable to create a worktree: "+err.Error())
		return
	}
	r.preparing, r.dir = false, w.Dir
	rn.Store.UpdateRun(id, r.started, func(run *Run) { run.Worktree, run.WorktreeState = &w, WorktreeActive })
	rn.host.AddWorkspace(w.Dir)
	a := rn.Store.Automations[i]
	thread := rn.Submit(model.SendRequest{Agent: a.Agent, Project: w.Dir, Text: RunMessage(a, prev) + worktreeNote(w), MemoryControl: true})
	if thread == 0 {
		rn.finish(id, Failed, model.AgentName(a.Agent)+" is not ready")
		return
	}
	rn.attach(id, a, r, thread)
	rn.Persist()
}

// settle looks at what a finished run left in its worktree: one that
// changed nothing is removed, one that did is kept for merge or discard.
// With notice, the run's notification follows, saying which.
func (rn *Runner) settle(id string, started time.Time, notice bool) {
	var w *worktree.Worktree
	rn.Store.UpdateRun(id, started, func(run *Run) { w = run.Worktree })
	if w == nil {
		return
	}
	wt := *w
	rn.Background(func() func() {
		ctx := context.Background()
		gone := !wt.Exists()
		var changes worktree.Changes
		var err error
		removed := false
		if !gone {
			if changes, err = wt.Changes(ctx); err == nil && changes.Empty() {
				removed = wt.Remove(ctx, false) == nil
			}
		}
		return func() {
			var run Run
			rn.Store.UpdateRun(id, started, func(x *Run) {
				x.Changes = changes
				switch {
				case gone:
					x.WorktreeState = WorktreeDiscarded
				case removed:
					x.WorktreeState = WorktreeCleaned
				default:
					x.WorktreeState = WorktreeKept
					if err != nil {
						x.WorktreeError = err.Error()
					}
				}
				run = *x
			})
			if gone || removed {
				rn.host.RemoveWorkspace(wt.Dir)
			}
			rn.Persist()
			rn.host.Changed()
			if i, ok := rn.Store.Find(id); ok && notice {
				rn.notice(rn.Store.Automations[i], run)
			}
		}
	})
}

// WorktreeAction merges or discards what a finished run left in its
// worktree. The work happens off the host's thread; the run shows
// WorktreeBusy until it ends, then the outcome or WorktreeError.
func (rn *Runner) WorktreeAction(id string, started time.Time, action string) error {
	if action != WorktreeMerge && action != WorktreeDiscard {
		return errors.New("Unknown worktree action: " + action)
	}
	i, ok := rn.Store.Find(id)
	if !ok {
		return errors.New("Automation not found")
	}
	if r := rn.Active[id]; r != nil && r.started.Equal(started) {
		return errors.New("The run has not finished")
	}
	var w *worktree.Worktree
	var state string
	if !rn.Store.UpdateRun(id, started, func(run *Run) { w, state = run.Worktree, run.WorktreeState }) {
		return errors.New("Run not found")
	}
	if w == nil || state != WorktreeKept && state != WorktreeActive {
		return errors.New("This run has no worktree to merge or discard")
	}
	wt, message := *w, rn.Store.Automations[i].Name+" · "+started.Format("Jan 2, 15:04")
	rn.Store.UpdateRun(id, started, func(run *Run) { run.WorktreeState, run.WorktreeError = WorktreeBusy, "" })
	rn.Persist()
	rn.host.Changed()
	rn.Background(func() func() {
		ctx := context.Background()
		var err error
		if action == WorktreeMerge {
			err = wt.Merge(ctx, "Miya automation: "+message)
		}
		if err == nil {
			err = wt.Remove(ctx, false)
		}
		return func() {
			rn.Store.UpdateRun(id, started, func(run *Run) {
				switch {
				case err != nil:
					run.WorktreeState, run.WorktreeError = WorktreeKept, err.Error()
				case action == WorktreeMerge:
					run.WorktreeState = WorktreeMerged
				default:
					run.WorktreeState = WorktreeDiscarded
				}
			})
			if err == nil {
				rn.host.RemoveWorkspace(wt.Dir)
			}
			rn.Persist()
			rn.host.Changed()
		}
	})
	return nil
}

// WorktreeLabel says what became of a run's worktree.
func WorktreeLabel(run Run) string {
	if run.Worktree == nil {
		return ""
	}
	switch run.WorktreeState {
	case WorktreeActive:
		return fmt.Sprintf(tr("Working on branch %s"), run.Worktree.Branch)
	case WorktreeKept:
		return fmt.Sprintf(tr("Changes on branch %s"), run.Worktree.Branch)
	case WorktreeCleaned:
		return tr("No changes; worktree removed")
	case WorktreeMerged:
		return fmt.Sprintf(tr("Merged branch %s"), run.Worktree.Branch)
	case WorktreeDiscarded:
		return tr("Worktree discarded")
	case WorktreeBusy:
		return tr("Updating worktree…")
	}
	return ""
}

// Workspace is where run worked: its worktree while it exists, otherwise
// the automation's project.
func Workspace(project string, run Run) string {
	if run.Worktree != nil && (run.WorktreeState == WorktreeActive || run.WorktreeState == WorktreeKept || run.WorktreeState == WorktreeBusy) {
		return run.Worktree.Dir
	}
	return project
}

// WorktreePending reports whether a run's worktree waits for merge or
// discard.
func WorktreePending(run Run) bool {
	return run.Worktree != nil && run.WorktreeState == WorktreeKept
}
