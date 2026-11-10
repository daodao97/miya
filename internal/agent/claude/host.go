package claude

import (
	"miya/internal/agent"
	native "miya/internal/agent/claude/native"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
)

type State struct {
	Binary, Error, Command, Model, Effort, DefaultModel string
	Args, Env                                           []string
	// Models are those Claude Code last reported, nil until it has.
	Models []native.Model
}

type Host interface {
	SetRun(int, *native.Client)
	HistoryLoading(chan struct{})
	ReserveExecutionEpoch(string, string) string
	NoteRuntime(model.Thread)
	ManagedStarted(int)
	ManagedResult(int, dg.State, string)
	NoteModel(string)
	// RefreshModels starts reading Claude Code's models when they are due.
	RefreshModels()
	agent.Workspace
	agent.Executions
	Snapshot() State
	Run(int) *native.Client
	Discover()
	RemoveThread(int, string)
	SetCommand(string)
	SetModel(string)
	SetEffort(string)
	Commands(project string, load bool) ([]model.ComposerChoice, bool)
}

type Adapter struct{ host Host }

func New(host Host) *Adapter { return &Adapter{host: host} }

var _ agent.Adapter = (*Adapter)(nil)
