package main

import (
	"context"
	"errors"
	"fmt"
	"miya/internal/remote"
	rt "miya/internal/runtime"
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
				out.Notice = "部分后台任务没有提供系统进程身份，相关端口无法归属"
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
			out.Tasks = append(out.Tasks, rt.Task{ID: task.ID, RunID: task.RunID, RunOrder: task.RunID, ToolID: task.ToolUseID, Kind: task.Kind, Description: rt.Summary(firstNonEmpty(task.Description, task.Command, "后台任务")), Status: task.Status})
		}
		if pid > 0 {
			out.Roots = []rt.Root{{PID: pid, TaskID: "_session"}}
		}
		if !known {
			out.Notice = "原生后台任务集合尚未确认；进程按当前会话的执行进程核验"
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
	if !ref.valid() || ref.Source != c.referenceSource {
		return nil, errors.New("对话运行信息属于另一台桌面或标识无效")
	}
	target := c.phoneThreadFor(remoteRuntimeRequest(ref, project))
	if !target.found {
		return nil, errors.New("对话不存在或已不在所选项目")
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
	result := conversationRuntime{View: view, Services: []phoneWebService{}}
	for _, e := range view.Endpoints {
		if e.Protocol != "http" {
			continue
		}
		name := "Web 服务"
		for _, p := range view.Processes {
			if p.Identity == e.Process {
				name = p.Name
				break
			}
		}
		result.Services = append(result.Services, phoneWebService{Port: e.Port, Host: e.Host, Process: name, RuntimeToken: manager.token(key, e), Owner: &ThreadRef{Source: key.Source, Backend: key.Backend, SessionID: key.SessionID}})
	}
	return result
}

// Deadline-free call sites use the same stable request identity as the phone.
func remoteRuntimeRequest(ref ThreadRef, project string) remote.Request {
	return remote.Request{ServerID: ref.SessionID, Agent: ref.Backend, Project: project}
}

type conversationRuntime struct {
	View     rt.View           `json:"view"`
	Services []phoneWebService `json:"services"`
}

func (c *chatController) readRuntime(ctx context.Context, ref ThreadRef, project string) (conversationRuntime, error) {
	if c.remote != nil {
		var result conversationRuntime
		err := c.remote.call("runtime", runtimeRequest{Ref: ref, Project: project}, &result)
		return result, err
	}
	type prepared struct {
		work deferred
		err  error
	}
	result, ok := onMain(ctx, c.dispatch, func() prepared { work, err := c.prepareRuntime(ref, project); return prepared{work, err} })
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
	return conversationRuntime{}, fmt.Errorf("无效的运行态数据")
}

type runtimeRequest struct {
	Ref     ThreadRef
	Project string
}
