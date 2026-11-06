package agent

import (
	"context"
	"encoding/json"

	dg "miya/internal/delegation"
	"miya/internal/interaction"
	"miya/internal/memory/manager"
	"miya/internal/model"
)

// Workspace is the application side of an integration. It owns conversation
// storage, dispatch, memory and orchestration; providers own native semantics.
type Workspace interface {
	AddThread(string, model.Thread)
	Enqueue(int, model.SendRequest) int
	DrainQueue(int)
	Upsert(*model.Thread, model.Message)
	Approvals() []interaction.Request
	SetApprovals([]interaction.Request)
	Context() context.Context
	Cwd() string
	ThreadWithID(int) model.Thread
	ThreadWorkspace(int) string
	ThreadRef(model.Thread) model.ThreadRef
	Threads() []model.Thread
	SetThreads([]model.Thread)
	Update(int, func(*model.Thread))
	NextID() int
	Dispatch(func())
	Memories() *manager.Manager
	Submit(model.SendRequest) int
	ReturnDraft(int, string, []string)
	FinishRewind(int, int, string, error, func(*model.Thread))
	PermissionForThread(model.Thread) string
	Respond(interaction.Request, any)
	RemoveRequest(json.RawMessage)
	SetStatus(string)
}

// Executions connects native lifecycle acknowledgements to application-owned
// delegation records without exposing the coordinator or its mutable state.
type Executions interface {
	BindExecution(context.Context, string, string, string) error
	ManagedEvent(string, dg.Event)
	Cancelled(error) bool
	FinishPausedDelivery(string, error) bool
}
