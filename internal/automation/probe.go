package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"miya/internal/textutil"
)

// ProbeResult is what a condition probe found.
type ProbeResult struct {
	Met bool
	// Value says what was found, as people read it.
	Value string
	// Amount is the measured amount, in GB, for the probes that have one.
	Amount float64
	Err    string
}

const gb = 1 << 30

// probeTimeout bounds a command probe.
const probeTimeout = 30 * time.Second

// runProbe probes c, with dir as a command probe's working directory.
func runProbe(c Condition, dir string) ProbeResult {
	switch c.Probe {
	case ProbeDiskFree:
		free, err := diskFree(c.Path)
		if err != nil {
			return ProbeResult{Err: err.Error()}
		}
		return amountResult(float64(free)/gb, c.BelowGB, fmt.Sprintf(tr("%s free on %s"), formatAmount(float64(free)/gb), c.Path))
	case ProbeMemoryFree:
		free, err := memoryFree()
		if err != nil {
			return ProbeResult{Err: err.Error()}
		}
		return amountResult(float64(free)/gb, c.BelowGB, fmt.Sprintf(tr("%s of memory available"), formatAmount(float64(free)/gb)))
	case ProbeCommand:
		return commandProbe(dir, c.Command)
	}
	return ProbeResult{Err: "unknown probe " + c.Probe}
}

func amountResult(amount, below float64, value string) ProbeResult {
	return ProbeResult{Met: amount < below, Amount: amount, Value: value}
}

func formatAmount(gbs float64) string {
	return strconv.FormatFloat(gbs, 'f', 1, 64) + " GB"
}

// rearmed reports whether a met condition has recovered far enough to
// wake again: a measured amount has to clear its threshold by a tenth, so
// that a value hovering at the threshold does not wake again and again.
func rearmed(c Condition, r ProbeResult) bool {
	if r.Met {
		return false
	}
	if c.Probe == ProbeCommand {
		return true
	}
	return r.Amount >= c.BelowGB*1.1
}

func commandProbe(dir, command string) ProbeResult {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, textutil.FirstNonEmpty(os.Getenv("SHELL"), "/bin/sh"), "-lc", command)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	value := textutil.ClipRunes(textutil.FirstLine(strings.TrimSpace(string(out))), 200)
	if ctx.Err() == context.DeadlineExceeded {
		return ProbeResult{Err: fmt.Sprintf("stopped after %s", probeTimeout)}
	}
	if err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			return ProbeResult{Err: err.Error()}
		}
		return ProbeResult{Value: value}
	}
	return ProbeResult{Met: true, Value: value}
}

// memoryFree is the memory that can be had without swapping, in bytes.
func memoryFree() (uint64, error) {
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemAvailable:" {
				kb, err := strconv.ParseUint(f[1], 10, 64)
				return kb * 1024, err
			}
		}
	}
	out, err := exec.Command("/usr/bin/vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("reading memory statistics: %v", err)
	}
	return parseVMStat(string(out))
}

// parseVMStat adds up the free, inactive, speculative and purgeable pages
// macOS's vm_stat reports: what applications can get without swapping.
func parseVMStat(out string) (uint64, error) {
	page := uint64(4096)
	var pages uint64
	found := false
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "page size of "); i >= 0 {
			if f := strings.Fields(line[i+len("page size of "):]); len(f) > 0 {
				if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
					page = n
				}
			}
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "Pages free", "Pages inactive", "Pages speculative", "Pages purgeable":
			n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("reading vm_stat: %v", err)
			}
			pages += n
			found = true
		}
	}
	if !found {
		return 0, fmt.Errorf("vm_stat reported no pages")
	}
	return pages * page, nil
}

// fileSnapshot is what a file trigger last saw: each entry's size and
// modification time.
type fileSnapshot map[string]string

// fileEntryLimit bounds the entries of a watched directory.
const fileEntryLimit = 5000

// snapshotPath reads a file, or the entries of a directory, without going
// into subdirectories. A missing path is an empty snapshot.
func snapshotPath(path string) (fileSnapshot, error) {
	snap := fileSnapshot{}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return snap, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		snap[filepath.Base(path)] = entryStamp(info)
		return snap, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for i, e := range entries {
		if i >= fileEntryLimit {
			break
		}
		if info, err := e.Info(); err == nil {
			snap[e.Name()] = entryStamp(info)
		}
	}
	return snap, nil
}

func entryStamp(info os.FileInfo) string {
	return strconv.FormatInt(info.Size(), 10) + "@" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// fingerprint identifies a snapshot, to compare with one from before the
// service restarted.
func (s fileSnapshot) fingerprint() string {
	names := make([]string, 0, len(s))
	for n := range s {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n + "\x00" + s[n] + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// fileChanges lists what changed from before to after.
func fileChanges(before, after fileSnapshot) (added, removed, modified []string) {
	for n, stamp := range after {
		if old, ok := before[n]; !ok {
			added = append(added, n)
		} else if old != stamp {
			modified = append(modified, n)
		}
	}
	for n := range before {
		if _, ok := after[n]; !ok {
			removed = append(removed, n)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(modified)
	return
}

// describeFileChanges says what changed, naming at most a few entries.
func describeFileChanges(added, removed, modified []string) string {
	var parts []string
	list := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		shown := names
		if len(shown) > 20 {
			shown = shown[:20]
		}
		line := fmt.Sprintf("%s (%d): %s", label, len(names), strings.Join(shown, ", "))
		if len(names) > len(shown) {
			line += ", …"
		}
		parts = append(parts, line)
	}
	list("Added", added)
	list("Removed", removed)
	list("Modified", modified)
	return strings.Join(parts, "\n")
}
