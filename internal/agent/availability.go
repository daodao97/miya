package agent

// Availability is an agent as settings show it: whether its CLI is
// installed, where, and whether new conversations may use it.
type Availability struct {
	ID, Name string
	// Path is the CLI found; Problem says why none was.
	Path, Problem string
	Enabled       bool
}

func (a Availability) Installed() bool { return a.Path != "" }
