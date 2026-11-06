//go:build !darwin && !linux

package quota

import "os/exec"

func isolateReader(cmd *exec.Cmd) {}
