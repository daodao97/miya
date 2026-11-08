//go:build windows

package codex

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}

// Windows has no SIGTERM for a console-less child; closing stdin is the request.
func interruptProcess(cmd *exec.Cmd) {}
func terminateProcess(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
