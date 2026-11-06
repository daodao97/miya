// Package agent defines the common contract implemented by every agent integration.
// Adapters are bound to one workspace; callers never pass a controller or native client.
package agent

import (
	"miya/internal/agentmeta"
	"miya/internal/codex"
	"miya/internal/model"
	"miya/internal/runtime/tracker"
)

type Setting struct {
	ID, Title, Help, Placeholder string
	Value                        func() string
	Save                         func(string)
	Status                       func() (string, bool)
}

// Controls is the view-facing portion of an adapter. Native clients and execution
// state remain behind this interface.
type Controls interface {
	Name() string
	Commands(project string, load bool) CommandCatalog
	Caps() agentmeta.Capabilities
	Problem() (string, bool)
	Reconnect()
	ModelChoices() []string
	ModelLabel(string) string
	EffortChoices() []string
	Model() string
	Effort() string
	SetModel(string)
	SetEffort(string)
}

// Adapter owns one agent's lifecycle and projects its native behavior onto the
// shared conversation model. A workspace creates a separate adapter per agent.
type Adapter interface {
	Controls
	ID() string
	Icon() agentmeta.Icon
	Runtime(model.Thread) tracker.SnapshotSource
	Discover()
	Ready() bool
	Open(model.Thread)
	Send(model.SendRequest) int
	Stop(model.Thread)
	Archive(model.Thread)
	Delete(model.Thread)
	Suspend(model.Thread)
	Owns(codex.Approval) bool
	Respond(codex.Approval, any)
	Settings() []Setting
	ModelOptions() []ModelOption
	CanRewind(model.Thread, model.Message) bool
	Rewind(model.Thread, model.Message, string)
	Steer(model.Thread, model.SendRequest) bool
}
