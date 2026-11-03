package main

import (
	"context"
	rt "miya/internal/runtime"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type runtimeObserver struct {
	mu  sync.Mutex
	at  time.Time
	all []rt.Process
	err error
}

// Only inspect same-user listening candidates, never the environment of the
// full process table. A detached service can retain Codex's execution marker
// after its shell and native background terminal have both ended.
func runtimeTaggedRoots(ctx context.Context, key rt.SessionKey, all []rt.Process) []rt.Root {
	if key.Backend != agentCodex || key.SessionID == "" {
		return nil
	}
	data, err := runWebLSOF(ctx, "-nP", "-a", "-u", strconv.Itoa(os.Getuid()), "-iTCP", "-sTCP:LISTEN", "-F0p")
	if err != nil {
		return nil
	}
	byPID := map[int]rt.Process{}
	for _, p := range all {
		byPID[p.Identity.PID] = p
	}
	var roots []rt.Root
	for _, field := range webLSOFFields(data) {
		if !strings.HasPrefix(field, "p") {
			continue
		}
		pid, err := strconv.Atoi(field[1:])
		p, ok := byPID[pid]
		if err != nil || !ok || ctx.Err() != nil {
			continue
		}
		if rt.CodexProcessSession(pid) == key.SessionID {
			roots = append(roots, rt.Root{PID: pid, TaskID: "_session-tag/" + strconv.Itoa(pid) + "/" + p.Identity.Start, Identity: p.Identity, Evidence: "session-tag"})
		}
	}
	return roots
}

func (o *runtimeObserver) processes(force bool) ([]rt.Process, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !force && time.Since(o.at) < 200*time.Millisecond {
		return o.all, o.err
	}
	o.all, o.err = rt.SystemProcesses()
	o.at = time.Now()
	return o.all, o.err
}
func runtimeListeners(ctx context.Context, processes []rt.Process) ([]rt.Endpoint, error) {
	owned := map[int]rt.Identity{}
	pids := []string{}
	for _, p := range processes {
		if p.Status == "running" {
			owned[p.Identity.PID] = p.Identity
			pids = append(pids, strconv.Itoa(p.Identity.PID))
		}
	}
	if len(pids) == 0 {
		return []rt.Endpoint{}, nil
	}
	data, err := runWebLSOF(ctx, "-nP", "-a", "-p", strings.Join(pids, ","), "-iTCP", "-sTCP:LISTEN", "-F0pcfnt")
	if err != nil {
		return nil, err
	}
	return rt.ParseListeners(data, owned), nil
}
