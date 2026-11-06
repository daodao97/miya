package codex

import (
	"context"

	"miya/internal/chat/model"
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
)

func (b Adapter) Runtime(t model.Thread) tracker.SnapshotSource {
	c := b.host
	client := c.Snapshot().Client
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
