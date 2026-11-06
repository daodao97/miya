package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"miya/internal/async"
	"miya/internal/model"
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
	"miya/internal/svcproto"
	"miya/internal/textutil"
)

func (CodexBackend) Runtime(c *Controller, t model.Thread) tracker.SnapshotSource {
	client := c.Client
	caps := rt.Capabilities{Tasks: true, Snapshot: true, NativePID: true, SessionTag: true, RequiresHost: true}
	if client == nil {
		return tracker.SnapshotSource{Epoch: "disconnected", Caps: caps, Alive: func() bool { return false }}
	}
	return tracker.SnapshotSource{Epoch: client.RuntimeEpoch(), Caps: caps, Alive: func() bool {
		select {
		case <-client.Done():
			return false
		default:
			return true
		}
	}, Snapshot: func(ctx context.Context) (tracker.TaskSnapshot, error) {
		terminals, err := client.BackgroundTerminals(ctx, t.ServerID)
		if err != nil {
			return tracker.TaskSnapshot{}, err
		}
		out := tracker.TaskSnapshot{Known: true, Tasks: []rt.Task{}}
		for _, terminal := range terminals {
			task := rt.Task{ID: terminal.ProcessID, ToolID: terminal.ItemID, RunID: terminal.ItemID, Kind: "shell", Description: rt.Summary(terminal.Command), Status: "running"}
			if task.ID == "" {
				task.ID = terminal.ItemID
			}
			if terminal.OSPID != nil && *terminal.OSPID > 0 {
				task.PID = *terminal.OSPID
				out.Roots = append(out.Roots, rt.Root{PID: task.PID, TaskID: task.ID + "/" + task.RunID})
			} else {
				out.Notice = "Some background tasks did not provide system process identities; their ports cannot be attributed"
			}
			out.Tasks = append(out.Tasks, task)
		}
		return out, nil
	}}
}

func (claudeBackend) Runtime(c *Controller, t model.Thread) tracker.SnapshotSource {
	client := c.ClaudeRuns[t.ID]
	caps := rt.Capabilities{Tasks: true, Snapshot: true, SessionRoot: true, RequiresHost: true}
	if client == nil {
		return tracker.SnapshotSource{Epoch: "disconnected", Caps: caps, Alive: func() bool { return false }}
	}
	_, _, _, epoch := client.Runtime()
	return tracker.SnapshotSource{Epoch: epoch, Caps: caps, Alive: func() bool {
		select {
		case <-client.Done():
			return false
		default:
			return true
		}
	}, Snapshot: func(context.Context) (tracker.TaskSnapshot, error) {
		tasks, known, pid, _ := client.Runtime()
		out := tracker.TaskSnapshot{Known: known, Tasks: []rt.Task{}}
		for _, task := range tasks {
			out.Tasks = append(out.Tasks, rt.Task{ID: task.ID, RunID: task.RunID, RunOrder: task.RunID, ToolID: task.ToolUseID, Kind: task.Kind, Description: rt.Summary(textutil.FirstNonEmpty(task.Description, task.Command, "Background tasks")), Status: task.Status})
		}
		if pid > 0 {
			out.Roots = []rt.Root{{PID: pid, TaskID: "_session"}}
		}
		if !known {
			out.Notice = "Native background tasks are unconfirmed. Processes are verified against the current session's execution processes."
		}
		return out, nil
	}}
}

func (c *Controller) noteRuntime(t model.Thread) {
	if t.ServerID == "" || c.Remote != nil || c.Closed {
		return
	}
	source := BackendOf(t).Runtime(c, t)
	if source.Epoch == "" || source.Epoch == "disconnected" {
		return
	}
	key := tracker.KeyOf(c.ThreadRef(t))
	manager := c.Runtimes()
	manager.Register(key, source)
	last := manager.LastObserved(key)
	if time.Since(last) >= 200*time.Millisecond {
		manager.Kick(key)
	}
}

// Used by UI, phone and MCP: resolve the stable session on the executor, then
// perform native RPC/system observation outside it.
func (c *Controller) PrepareRuntime(ref model.ThreadRef, project string) (async.Reply, error) {
	if !ref.Valid() || ref.Source != c.ReferenceSource {
		return nil, errors.New("Conversation runtime belongs to another desktop or has an invalid identity")
	}
	target := c.PhoneThreadFor(tracker.RemoteRuntimeRequest(ref, project))
	if !target.Found {
		return nil, errors.New("Conversation does not exist or is no longer in the selected project")
	}
	source := BackendOf(target.Thread).Runtime(c, target.Thread)
	manager := c.Runtimes()
	key := tracker.KeyOf(ref)
	manager.Register(key, source)
	return func() (any, error) {
		view := manager.Refresh(manager.Context(), key)
		return tracker.Result(manager, key, view), nil
	}, nil
}

func (c *Controller) ReadRuntime(ctx context.Context, ref model.ThreadRef, project string) (tracker.ConversationRuntime, error) {
	cl, ok := async.OnMain(ctx, c.Dispatch, func() *svcproto.Client { return c.Remote })
	if !ok {
		return tracker.ConversationRuntime{}, ctx.Err()
	}
	if cl != nil {
		var result tracker.ConversationRuntime
		err := cl.Call("runtime", tracker.Request{Ref: ref, Project: project}, &result)
		return result, err
	}
	type prepared struct {
		work async.Reply
		err  error
	}
	result, ok := async.OnMain(ctx, c.Dispatch, func() prepared { work, err := c.PrepareRuntime(ref, project); return prepared{work, err} })
	if !ok {
		return tracker.ConversationRuntime{}, ctx.Err()
	}
	if result.err != nil {
		return tracker.ConversationRuntime{}, result.err
	}
	data, err := result.work()
	if err != nil {
		return tracker.ConversationRuntime{}, err
	}
	if value, ok := data.(tracker.ConversationRuntime); ok {
		return value, nil
	}
	return tracker.ConversationRuntime{}, fmt.Errorf("Invalid runtime data")
}
