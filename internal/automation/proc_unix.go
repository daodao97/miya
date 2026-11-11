//go:build unix

package automation

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ownGroup gives a command a process group of its own and makes cancelling
// it end the whole group: a shell command usually runs other programs, and
// a pipeline's would otherwise outlive it. The group is asked to exit, then
// killed after groupGrace.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		err := syscall.Kill(-pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		time.AfterFunc(groupGrace, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		return err
	}
}
