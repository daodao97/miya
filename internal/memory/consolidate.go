package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Consolidation merges duplicate memories of one project and marks apparent
// contradictions for the user. A project qualifies once automatic extraction
// is on, it has no open extraction job, and its memory has been quiet for
// ConsolidationIdle. It shares the extraction engine and agent, so it never
// runs beside an extraction, and it is not a fixed nightly task.
//
// The model proposes; this file decides. Every operation names IDs and
// versions that were offered to it, so an explicit edit made while it ran
// voids only the operations touching the edited entries. Nothing is deleted:
// a merged entry keeps its body as history and is no longer recalled, and a
// contradiction is left to the user as a conflict.
const (
	ConsolidationIdle         = 6 * time.Hour
	consolidationRetryAfter   = 30 * time.Minute
	consolidationAttempts     = 2
	maxConsolidationEntries   = 60
	maxConsolidationTargets   = 4
	MaxConsolidationMerges    = 5
	MaxConsolidationConflicts = 3
)

type ConsolidationTarget struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
}

// ConsolidationOp is one proposal. merge rewrites its targets as the single
// conclusion in Content; conflict marks them for the user to verify.
type ConsolidationOp struct {
	Action  string                `json:"action"`
	Targets []ConsolidationTarget `json:"targets"`
	Content string                `json:"content"`
	Reason  string                `json:"reason"`
}

type ConsolidationEntry struct {
	ID         string    `json:"id"`
	Version    uint64    `json:"version"`
	Kind       string    `json:"kind"`
	Content    string    `json:"content"`
	ObservedAt time.Time `json:"observedAt"`
}

// ConsolidationJob is the model input: memory bodies, never source excerpts.
type ConsolidationJob struct {
	ExtractorAgent string               `json:"-"`
	Key            string               `json:"-"`
	Now            time.Time            `json:"now"`
	Entries        []ConsolidationEntry `json:"entries"`
}

func (j ConsolidationJob) InputJSON() ([]byte, error) {
	b, err := json.Marshal(j)
	if err == nil && utf8.RuneCount(b) > MaxExtractionInput {
		return nil, fmt.Errorf("Consolidation input exceeds %d characters", MaxExtractionInput)
	}
	return b, err
}

// consolidation is a scope's persisted state. Offered lists the running
// call's targets without content; Covered fingerprints the eligible entries
// the last finished run saw, so an unchanged library is not consolidated
// again.
type consolidation struct {
	State       JobState              `json:"state,omitempty"`
	Key         string                `json:"key,omitempty"`
	Offered     []ConsolidationTarget `json:"offered,omitempty"`
	Attempts    int                   `json:"attempts,omitempty"`
	Covered     string                `json:"covered,omitempty"`
	CompletedAt time.Time             `json:"completedAt,omitzero"`
	DurationMs  int64                 `json:"durationMs,omitempty"`
	Merged      int                   `json:"merged,omitempty"`
	Conflicts   int                   `json:"conflicts,omitempty"`
	Rejected    int                   `json:"rejected,omitempty"`
	Error       string                `json:"error,omitempty"`
	Detail      string                `json:"detail,omitempty"`
}

func consolidationEligible(d document, now time.Time) []Entry {
	out := []Entry{}
	for _, e := range latest(d) {
		e = effective(e, now)
		if e.State == EntryActive && e.Kind != "progress" {
			out = append(out, e)
		}
	}
	return out
}

func coverage(entries []Entry) string {
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, fmt.Sprintf("%s@%d", e.ID, e.Version))
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(sum[:])
}

// lastActivity is when the scope's memory last changed or was extracted. An
// open extraction job means the project is not quiet.
func lastActivity(d document) (time.Time, bool) {
	var last time.Time
	later := func(t time.Time) {
		if t.After(last) {
			last = t
		}
	}
	for _, j := range d.Jobs {
		if j.State.Open() {
			return time.Time{}, false
		}
		later(j.CreatedAt)
		later(j.CompletedAt)
	}
	for _, e := range d.Entries {
		later(e.UpdatedAt)
	}
	return last, true
}

func consolidationDue(d document, now time.Time) bool {
	c := d.Consolidation
	if d.Scope.Kind != "project" || d.Settings.Disabled || !d.Settings.AutoExtract || c.State == JobRunning {
		return false
	}
	if c.Error != "" && now.Before(c.CompletedAt.Add(consolidationRetryAfter)) {
		return false
	}
	eligible := consolidationEligible(d, now)
	if len(eligible) < 2 || coverage(eligible) == c.Covered {
		return false
	}
	last, quiet := lastActivity(d)
	return quiet && now.Sub(last) >= ConsolidationIdle
}

// ClaimConsolidation starts the first due project's run. The newest entries
// that fit the input budget are offered; the others wait for a later change.
func (s *Store) ClaimConsolidation() (Scope, ConsolidationJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, scope := range s.scopesOnDisk() {
		d, err := s.load(scope)
		if err != nil || !consolidationDue(d, now) {
			continue
		}
		eligible := consolidationEligible(d, now)
		sort.SliceStable(eligible, func(i, j int) bool {
			if !eligible[i].ObservedAt.Equal(eligible[j].ObservedAt) {
				return eligible[i].ObservedAt.After(eligible[j].ObservedAt)
			}
			return eligible[i].ID < eligible[j].ID
		})
		job := ConsolidationJob{ExtractorAgent: d.Settings.ExtractionAgent(), Now: now, Entries: []ConsolidationEntry{}}
		var offered []ConsolidationTarget
		for _, e := range eligible {
			if len(job.Entries) == maxConsolidationEntries {
				break
			}
			job.Entries = append(job.Entries, ConsolidationEntry{ID: e.ID, Version: e.Version, Kind: e.Kind, Content: e.Content, ObservedAt: e.ObservedAt})
			if _, err := job.InputJSON(); err != nil {
				job.Entries = job.Entries[:len(job.Entries)-1]
				break
			}
			offered = append(offered, ConsolidationTarget{ID: e.ID, Version: e.Version})
		}
		if len(offered) < 2 {
			d.Consolidation.Covered = coverage(eligible)
			if err := s.commit(d); err != nil {
				return Scope{}, ConsolidationJob{}, false, err
			}
			continue
		}
		key, err := newID()
		if err != nil {
			return Scope{}, ConsolidationJob{}, false, err
		}
		job.Key = key
		c := &d.Consolidation
		c.State, c.Key, c.Offered = JobRunning, key, offered
		c.Attempts++
		if err := s.commit(d); err != nil {
			return Scope{}, ConsolidationJob{}, false, err
		}
		return scope, job, true, nil
	}
	return Scope{}, ConsolidationJob{}, false, nil
}

// CompleteConsolidation applies the operations that still pass every check
// and returns the entry versions it wrote.
func (s *Store) CompleteConsolidation(scope Scope, key string, ops []ConsolidationOp, duration time.Duration, runError error, requeue bool) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(scope)
	if err != nil {
		return nil, err
	}
	c := &d.Consolidation
	if c.State != JobRunning || c.Key != key {
		return nil, nil
	}
	offered := c.Offered
	c.State, c.Key, c.Offered = "", "", nil
	c.DurationMs = duration.Milliseconds()
	now := s.now()
	if runError != nil {
		if requeue {
			c.Attempts--
			return nil, s.commit(d)
		}
		c.CompletedAt = now
		c.Error, c.Detail = "Consolidation failed. Waiting to retry.", jobErrorDetail(runError)
		if c.Attempts >= consolidationAttempts {
			c.Error = "Consolidation failed. It runs again after memories change."
			c.Covered = coverage(consolidationEligible(d, now))
			c.Attempts = 0
		}
		return nil, s.commit(d)
	}
	c.CompletedAt, c.Attempts, c.Error, c.Detail = now, 0, "", ""
	c.Merged, c.Conflicts, c.Rejected = 0, 0, 0
	var accepted []Entry
	if !d.Settings.Disabled && d.Settings.AutoExtract {
		accepted, c.Merged, c.Conflicts, c.Rejected = applyConsolidation(&d, offered, ops, now)
	}
	d.Consolidation.Covered = coverage(consolidationEligible(d, now))
	if err := s.commit(d); err != nil {
		return nil, err
	}
	return accepted, nil
}

func applyConsolidation(d *document, offered []ConsolidationTarget, ops []ConsolidationOp, now time.Time) (accepted []Entry, merged, conflicts, rejected int) {
	used := map[string]bool{}
	for _, op := range ops {
		idx, err := consolidationTargets(*d, offered, op, used, now)
		if err == nil {
			switch {
			case op.Action == "merge" && merged < MaxConsolidationMerges:
				var written []Entry
				if written, err = mergeEntries(d, idx, op.Content, now); err == nil {
					accepted = append(accepted, written...)
					merged++
				}
			case op.Action == "conflict" && conflicts < MaxConsolidationConflicts:
				accepted = append(accepted, conflictEntries(d, idx, now)...)
				conflicts++
			default:
				err = errors.New("Unsupported or excess consolidation operation")
			}
		}
		if err != nil {
			rejected++
			continue
		}
		for _, t := range op.Targets {
			used[t.ID] = true
		}
	}
	return accepted, merged, conflicts, rejected
}

func consolidationTargets(d document, offered []ConsolidationTarget, op ConsolidationOp, used map[string]bool, now time.Time) ([]int, error) {
	if len(op.Targets) < 2 || len(op.Targets) > maxConsolidationTargets {
		return nil, errors.New("A consolidation operation needs 2 to 4 memories")
	}
	seen := map[string]bool{}
	var out []int
	for _, t := range op.Targets {
		if seen[t.ID] || used[t.ID] {
			return nil, errors.New("A memory can take part in only one consolidation operation")
		}
		seen[t.ID] = true
		if !slices.Contains(offered, t) {
			return nil, errors.New("The consolidation target was not offered")
		}
		i, err := target(d, t.ID, t.Version)
		if err != nil {
			return nil, err
		}
		e := effective(d.Entries[i], now)
		if e.State != EntryActive || e.Kind == "progress" {
			return nil, errors.New("Only active memories other than progress can be consolidated")
		}
		if op.Action == "merge" && len(out) > 0 && d.Entries[out[0]].Kind != e.Kind {
			return nil, errors.New("Only memories of the same kind can be merged")
		}
		out = append(out, i)
	}
	return out, nil
}

// mergeEntries keeps the oldest target's ID with the merged content and the
// sources of all targets, and marks the others merged into it.
func mergeEntries(d *document, idx []int, content string, now time.Time) ([]Entry, error) {
	content = strings.TrimSpace(content)
	var originals strings.Builder
	for _, i := range idx {
		originals.WriteString(normalized(d.Entries[i].Content))
		originals.WriteByte('\n')
	}
	for _, token := range identifierTokens(content) {
		if !strings.Contains(originals.String(), normalized(token)) {
			return nil, fmt.Errorf("Merged content introduces %q, which no merged memory contains", token)
		}
	}
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	for _, mark := range d.Tombstones {
		if slices.Contains(mark.Digests, digest) {
			return nil, errors.New("Forgotten content cannot be restored automatically")
		}
	}
	keep := idx[0]
	for _, i := range idx[1:] {
		a, b := d.Entries[i], d.Entries[keep]
		if a.CreatedAt.Before(b.CreatedAt) || a.CreatedAt.Equal(b.CreatedAt) && a.ID < b.ID {
			keep = i
		}
	}
	old := d.Entries[keep]
	e := old
	e.Version, e.Supersedes, e.Content, e.UpdatedAt = old.Version+1, old.Version, content, now
	e.Sources = mergedSources(*d, keep, idx)
	forever := false
	for _, i := range idx {
		o := d.Entries[i]
		if o.ObservedAt.After(e.ObservedAt) {
			e.ObservedAt = o.ObservedAt
		}
		if o.ValidUntil.IsZero() {
			forever = true
		} else if o.ValidUntil.After(e.ValidUntil) {
			e.ValidUntil = o.ValidUntil
		}
	}
	if forever {
		e.ValidUntil = time.Time{}
	}
	if err := validate(Mutation{ID: e.ID, Version: e.Version, Kind: e.Kind, Content: e.Content, TaskKey: e.TaskKey, Sources: e.Sources, ObservedAt: e.ObservedAt, ValidUntil: e.ValidUntil}, d.Scope); err != nil {
		return nil, err
	}
	var out []Entry
	for _, i := range idx {
		if i == keep {
			continue
		}
		o := d.Entries[i]
		d.Entries[i].State = EntrySuperseded
		m := o
		m.Version, m.Supersedes, m.State, m.MergedInto, m.UpdatedAt = o.Version+1, o.Version, EntryMerged, old.ID, now
		d.Entries = append(d.Entries, m)
		out = append(out, m)
	}
	d.Entries[keep].State = EntrySuperseded
	d.Entries = append(d.Entries, e)
	return append(out, e), nil
}

// mergedSources keeps the first source of every target before the rest, so
// each merged memory stays traceable within the limit of eight.
func mergedSources(d document, keep int, idx []int) []Source {
	order := []int{keep}
	for _, i := range idx {
		if i != keep {
			order = append(order, i)
		}
	}
	var firsts, rest []Source
	for _, i := range order {
		if srcs := d.Entries[i].Sources; len(srcs) > 0 {
			firsts = append(firsts, srcs[0])
			rest = append(rest, srcs[1:]...)
		}
	}
	out := []Source{}
	for _, src := range append(firsts, rest...) {
		if len(out) == 8 {
			break
		}
		if !slices.ContainsFunc(out, func(o Source) bool { return o == src || sameSource(o, src) }) {
			out = append(out, src)
		}
	}
	return out
}

func conflictEntries(d *document, idx []int, now time.Time) []Entry {
	var out []Entry
	for _, i := range idx {
		old := d.Entries[i]
		d.Entries[i].State = EntrySuperseded
		c := old
		c.Version, c.Supersedes, c.State, c.UpdatedAt = old.Version+1, old.Version, EntryConflicted, now
		d.Entries = append(d.Entries, c)
		out = append(out, c)
	}
	return out
}

// identifierTokens finds the ASCII words a merge must copy rather than invent:
// those with digits, underscores or @, interior dots, slashes or colons, or
// camelCase. Prose words and Chinese text may be reworded.
func identifierTokens(s string) []string {
	var out []string
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r >= utf8.RuneSelf || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.-/:@", r))
	})
	for _, f := range fields {
		if f = strings.Trim(f, ".-/:@"); f != "" && identifierLike(f) {
			out = append(out, f)
		}
	}
	return out
}

func identifierLike(s string) bool {
	prevLower := false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r), r == '_', r == '@', r == '.', r == '/', r == ':':
			return true
		case unicode.IsUpper(r) && prevLower:
			return true
		}
		prevLower = unicode.IsLower(r)
	}
	return false
}
