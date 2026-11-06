package cli

// Config describes one owned CLI invocation.
type Config struct {
	Binary    string
	Args, Env []string
}
