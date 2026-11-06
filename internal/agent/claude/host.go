package claude

import (
	"miya/internal/agent"
	native "miya/internal/agent/claude/native"
	dg "miya/internal/delegation"
	"miya/internal/model"
)

type State struct {
	Binary, Error, Command, Model, Effort, DefaultModel string
	Args, Env                                           []string
}

type Host interface {
	SetRun(int, *native.Client)
	HistoryLoading(chan struct{})
	ReserveExecutionEpoch(string, string) string
	NoteRuntime(model.Thread)
	ManagedStarted(int)
	ManagedResult(int, dg.State, string)
	NoteModel(string)
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
