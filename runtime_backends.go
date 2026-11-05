package main

import (
	"context"
	"errors"
	"fmt"
	"miya/internal/async"
	"miya/internal/remote"
	rt "miya/internal/runtime"
	"miya/internal/textutil"
	"miya/internal/webservice"
	"time"
)

func (codexBackend) Runtime(c *chatController, t Thread) runtimeSource {
	client := c.client
	caps := rt.Capabilities{Tasks: true, Snapshot: true, NativePID: true, SessionTag: true, RequiresHost: true}
	if client == nil {
		return runtimeSource{Epoch: "disconnected", Caps: caps, Alive: func() bool { return false }}
	}
	return runtimeSource{Epoch: client.RuntimeEpoch(), Caps: caps, Alive: func() bool {
		select {
		case <-client.Done():
			return false
		default:
			return true
		}
	}, Snapshot: func(ctx context.Context) (runtimeSnapshot, error) {
		terminals, err := client.BackgroundTerminals(ctx, t.ServerID)
		if err != nil {
			return runtimeSnapshot{}, err
		}
		out := runtimeSnapshot{Known: true, Tasks: []rt.Task{}}
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
func (claudeBackend) Runtime(c *chatController, t Thread) runtimeSource {
	client := c.claudeRuns[t.ID]
	caps := rt.Capabilities{Tasks: true, Snapshot: true, SessionRoot: true, RequiresHost: true}
	if client == nil {
		return runtimeSource{Epoch: "disconnected", Caps: caps, Alive: func() bool { return false }}
	}
	_, _, _, epoch := client.Runtime()
	return runtimeSource{Epoch: epoch, Caps: caps, Alive: func() bool {
		select {
		case <-client.Done():
			return false
		default:
			return true
		}
	}, Snapshot: func(context.Context) (runtimeSnapshot, error) {
		tasks, known, pid, _ := client.Runtime()
		out := runtimeSnapshot{Known: known, Tasks: []rt.Task{}}
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
func (c *chatController) noteRuntime(t Thread) {
	if t.ServerID == "" || c.remote != nil || c.closed {
		return
	}
	source := backendOf(t).Runtime(c, t)
	if source.Epoch == "" || source.Epoch == "disconnected" {
		return
	}
	key := runtimeKey(c.threadRef(t))
	manager := c.runtimes()
	manager.register(key, source)
	manager.mu.Lock()
	last := manager.sources[key].last
	manager.mu.Unlock()
	if time.Since(last) >= 200*time.Millisecond {
		manager.kick(key)
	}
}

// Used by UI, phone and MCP: resolve the stable session on the executor, then
// perform native RPC/system observation outside it.
func (c *chatController) prepareRuntime(ref ThreadRef, project string) (deferred, error) {
	if !ref.Valid() || ref.Source != c.referenceSource {
		return nil, errors.New("Conversation runtime belongs to another desktop or has an invalid identity")
	}
	target := c.phoneThreadFor(remoteRuntimeRequest(ref, project))
	if !target.found {
		return nil, errors.New("Conversation does not exist or is no longer in the selected project")
	}
	source := backendOf(target.thread).Runtime(c, target.thread)
	manager := c.runtimes()
	key := runtimeKey(ref)
	manager.register(key, source)
	return func() (any, error) {
		view := manager.refresh(manager.ctx, key)
		return runtimeResult(manager, key, view), nil
	}, nil
}
func runtimeResult(manager *runtimeManager, key rt.SessionKey, view rt.View) conversationRuntime {
	result := conversationRuntime{View: view, Services: []webservice.Service{}}
	for _, e := range view.Endpoints {
		if e.Protocol != "http" {
			continue
		}
		name := "Web services"
		for _, p := range view.Processes {
			if p.Identity == e.Process {
				name = p.Name
				break
			}
		}
		result.Services = append(result.Services, webservice.Service{Port: e.Port, Host: e.Host, Process: name, RuntimeToken: manager.token(key, e), Owner: &ThreadRef{Source: key.Source, Backend: key.Backend, SessionID: key.SessionID}})
	}
	return result
}

// Deadline-free call sites use the same stable request identity as the phone.
func remoteRuntimeRequest(ref ThreadRef, project string) remote.Request {
	return remote.Request{ServerID: ref.SessionID, Agent: ref.Backend, Project: project}
}

type conversationRuntime struct {
	View     rt.View              `json:"view"`
	Services []webservice.Service `json:"services"`
}

func (c *chatController) readRuntime(ctx context.Context, ref ThreadRef, project string) (conversationRuntime, error) {
	cl, ok := async.OnMain(ctx, c.dispatch, func() *serviceClient { return c.remote })
	if !ok {
		return conversationRuntime{}, ctx.Err()
	}
	if cl != nil {
		var result conversationRuntime
		err := cl.Call("runtime", runtimeRequest{Ref: ref, Project: project}, &result)
		return result, err
	}
	type prepared struct {
		work deferred
		err  error
	}
	result, ok := async.OnMain(ctx, c.dispatch, func() prepared { work, err := c.prepareRuntime(ref, project); return prepared{work, err} })
	if !ok {
		return conversationRuntime{}, ctx.Err()
	}
	if result.err != nil {
		return conversationRuntime{}, result.err
	}
	data, err := result.work()
	if err != nil {
		return conversationRuntime{}, err
	}
	if value, ok := data.(conversationRuntime); ok {
		return value, nil
	}
	return conversationRuntime{}, fmt.Errorf("Invalid runtime data")
}

type runtimeRequest struct {
	Ref     ThreadRef
	Project string
}
