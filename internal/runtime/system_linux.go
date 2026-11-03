package runtime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func SystemProcesses() ([]Process, error) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	out := []Process{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		raw := string(data)
		left, right := strings.Index(raw, "("), strings.LastIndex(raw, ")")
		if left < 0 || right < left {
			continue
		}
		fields := strings.Fields(raw[right+1:])
		if len(fields) < 20 || fields[0] == "Z" {
			continue
		}
		parent, _ := strconv.Atoi(fields[1])
		out = append(out, Process{Identity: Identity{Host: host, Boot: strings.TrimSpace(string(boot)), PID: pid, Start: fields[19]}, ParentPID: parent, Name: raw[left+1 : right]})
	}
	return out, nil
}
