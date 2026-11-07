//go:build !windows

package pi

import (
	"os/exec"
	"syscall"
)

// configureProcess puts pi and the tools it runs in their own group, so
// stopping it stops them.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
