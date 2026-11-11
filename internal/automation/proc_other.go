//go:build !unix

package automation

import "os/exec"

// ownGroup leaves the command as it is: cancelling it kills the shell.
func ownGroup(cmd *exec.Cmd) {}
