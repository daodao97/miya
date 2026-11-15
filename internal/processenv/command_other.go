//go:build !windows

package processenv

import "os/exec"

func prepareNative(cmd *exec.Cmd) {}

func platformDirs(home string) []string { return nil }
