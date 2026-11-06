package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/runtime/tracker"
	svcclient "miya/internal/service/client"
)

func (c *Controller) noteRuntime(t model.Thread) {
	if t.ServerID == "" || c.Remote != nil || c.Closed {
		return
	}
	source := c.BackendOf(t).Runtime(t)
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
	source := c.BackendOf(target.Thread).Runtime(target.Thread)
	manager := c.Runtimes()
	key := tracker.KeyOf(ref)
	manager.Register(key, source)
	return func() (any, error) {
		view := manager.Refresh(manager.Context(), key)
		return tracker.Result(manager, key, view), nil
	}, nil
}

func (c *Controller) ReadRuntime(ctx context.Context, ref model.ThreadRef, project string) (tracker.ConversationRuntime, error) {
	cl, ok := async.OnMain(ctx, c.Dispatch, func() *svcclient.Client { return c.Remote })
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
