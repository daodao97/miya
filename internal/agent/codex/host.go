package codex

import (
	"miya/internal/agent"
	native "miya/internal/agent/codex/native"
	"miya/internal/chat/model"
	"miya/internal/interaction"
)

// State is a read of the workspace's current native connection and selection.
// Fetch it for each operation; never retain it as a second mutable state store.
type State struct {
	Client                *native.Client
	Ready                 bool
	Status, Model, Effort string
	Models                []native.Model
	ModelsKnown           bool
}

type Host interface {
	RemoveThread(int, string)
	FindThread(string) model.Thread
	NoteRuntime(model.Thread)
	AccountUpdated(native.Frame)
	ManagedCodexOutput(model.Thread, string, native.Event)
	RememberedMCPAccess(interaction.Request) bool
	BeforeInputFailed(string, string)
	RefreshGoal(int)
	ManagedCodexAccepted(string, string, string, error, bool)
	agent.Workspace
	agent.Executions
	Snapshot() State
	Connect()
	AttachmentInputs([]string) []map[string]any
	SelectModel(string)
	SelectEffort(string)
	GoalCommand(model.SendRequest) (int, string)
	SetFastMode(bool)
}

type Adapter struct{ host Host }

func New(host Host) *Adapter { return &Adapter{host: host} }

var _ agent.Adapter = (*Adapter)(nil)
