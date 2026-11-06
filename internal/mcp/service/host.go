// Package service handles MCP requests on the background executor. It owns
// tool routing and caller verification, and reaches application state via Host.
package service

import (
	"encoding/json"

	"miya/internal/async"
	"miya/internal/automation"
	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
	"miya/internal/interaction"
	"miya/internal/memory/manager"
	"miya/internal/model"
)

type Host interface {
	Cwd() string
	ReferenceSource() string
	AllThreads() []model.Thread
	ProjectThreads(string) []model.Thread
	ThreadWithID(int) model.Thread
	ThreadWorkspace(int) string
	ThreadRef(model.Thread) model.ThreadRef
	ThreadTitle(model.Thread) string
	ResolveThread(int) (model.Thread, []model.Message, bool)
	ResolveReference(model.ThreadRef) (model.Thread, []model.Message, error)
	ReferenceState(model.ThreadRef) (known, loading bool)
	PrefetchThread(int)
	ThreadPacket(model.Thread, []model.Message) string
	HandoffState(int) (string, string)
	Submit(model.SendRequest) int
	LastSubmitError() string
	ParentOf(int) (int, bool)
	NoteDelegation(model.Thread, int, string)
	PrepareRuntime(model.ThreadRef, string) (async.Reply, error)
	Memories() *manager.Manager
	Collaboration() *coordinator.Coordinator
	StartCollaboration(model.Thread, string, coordinator.Args) (any, error)
	PrepareDelegation(model.Thread, coordinator.Args) (dg.StartInput, error)
	AgentList() map[string]any
	Automations() *automation.Runner
	AutomationOp(string, automation.Request) (any, error)
	AskUser(model.Thread, string, string) (json.RawMessage, chan bool)
	WaitUser(json.RawMessage, chan bool) bool
	OnMain(func() (any, error)) (any, error)
	Dispatch(func())
	Approvals() []interaction.Request
	SetApprovals([]interaction.Request)
}

type Service struct{ host Host }

func New(host Host) *Service { return &Service{host: host} }
