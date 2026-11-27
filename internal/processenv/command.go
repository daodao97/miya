package processenv

import (
	"context"
	"os/exec"
)

// Command and CommandContext keep native executables and Windows batch
// launchers behind the same process boundary. WSL commands are prepared only
// after the caller has supplied its working directory and environment.
func Command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

func Prepare(cmd *exec.Cmd) {
	if IsWSL(cmd.Path) {
		prepareWSL(cmd)
	}
	prepareNative(cmd)
}
