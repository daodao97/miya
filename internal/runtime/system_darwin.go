//go:build darwin

package runtime

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func SystemProcesses() ([]Process, error) {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return nil, err
	}
	entries, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	out := make([]Process, 0, len(entries))
	for _, entry := range entries {
		if entry.Proc.P_stat == 5 {
			continue
		} // zombies are not running resources
		name := unix.ByteSliceToString(entry.Proc.P_comm[:])
		out = append(out, Process{Identity: Identity{Host: host, Boot: fmt.Sprintf("%d.%06d", boot.Sec, boot.Usec), PID: int(entry.Proc.P_pid), Start: fmt.Sprintf("%d.%06d", entry.Proc.P_starttime.Sec, entry.Proc.P_starttime.Usec)}, ParentPID: int(entry.Eproc.Ppid), Name: name})
	}
	return out, nil
}
