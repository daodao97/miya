package integrations

import (
	"miya/internal/agent/codex/projects"
)

// Projects composes trusted-project sources without starting agent processes.
func Projects() []string { return projects.Trusted() }

func RememberProject(path string) { projects.Trust(path) }
func ForgetProject(path string)   { projects.Forget(path) }
