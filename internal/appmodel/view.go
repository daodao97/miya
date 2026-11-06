package appmodel

type AgentCaps struct {
	Models         bool // the model picker
	Effort         bool // the reasoning effort picker
	Quota          bool // the account usage ring
	ThreadCommands bool // /compact and /review
	StallHint      bool // "no answer for a while" beside a run
}

// AgentControls binds controls to one controller without exposing its backend.
type AgentControls struct {
	Name          func() string
	Caps          func() AgentCaps
	Problem       func() (string, bool)
	Reconnect     func()
	ModelChoices  func() []string
	ModelLabel    func(string) string
	EffortChoices func() []string
	Model         func() string
	Effort        func() string
	SetModel      func(string)
	SetEffort     func(string)
}
type SettingControls struct {
	ID, Title, Help, Placeholder string
	Value                        func() string
	Save                         func(string)
	Status                       func() (string, bool)
}
