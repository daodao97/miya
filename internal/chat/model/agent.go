package model

// AgentStatus is an agent backend's state at one moment, as features
// outside the controller see it. The controller makes a new one on each
// call; keep it no longer than the work on the controller's thread that
// asked for it.
type AgentStatus struct {
	ID, Name                    string
	Ready                       bool
	Model, Effort               string
	ModelChoices, EffortChoices []string
}
