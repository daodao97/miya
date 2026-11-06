//go:build !darwin && !linux

package cli

import "os/exec"

func IsolateReader(cmd *exec.Cmd) {}
