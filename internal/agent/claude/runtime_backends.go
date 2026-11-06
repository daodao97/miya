package claude

import (
	"context"

	"miya/internal/model"
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
	"miya/internal/textutil"
)

func (b Adapter) Runtime(t model.Thread) tracker.SnapshotSource {
	c := b.host
	client := c.Run(t.ID)
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
