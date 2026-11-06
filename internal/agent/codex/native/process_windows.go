//go:build windows

package codex

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}
func terminateProcess(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
