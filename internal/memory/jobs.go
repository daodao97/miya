package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxExtractionInput = 16000

// ExtractionEvidenceBudget is what a turn adapter may spend when choosing
// evidence. The remainder covers queue identity, related stamps, claim
// metadata and retry status, so adapter choices survive the later checks.
const ExtractionEvidenceBudget = MaxExtractionInput - 2048

func (j ExtractionJob) FitsEvidenceBudget() bool {
	b, err := json.Marshal(j)
	return err == nil && utf8.RuneCount(b) <= ExtractionEvidenceBudget
}

type Evidence struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Source Source `json:"source"`
}

type ExtractionJob struct {
	ExtractorAgent string     `json:"extractorAgent,omitempty"`
	Key            string     `json:"key"`
	Session        string     `json:"session"`
	TurnID         string     `json:"turnId"`
	TaskKey        string     `json:"taskKey"`
	Evidence       []Evidence `json:"evidence,omitempty"`
	Related        []Stamp    `json:"related,omitempty"`
	Candidates     []Entry    `json:"candidates,omitempty"`
	Fingerprint    string     `json:"fingerprint,omitempty"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    time.Time  `json:"completedAt,omitzero"`
	DurationMs     int64      `json:"durationMs,omitempty"`
	Error          string     `json:"error,omitempty"`
	Accepted       int        `json:"accepted,omitempty"`
	Rejected       int        `json:"rejected,omitempty"`
}

type Candidate struct {
	Action     string `json:"action"`
	ID         string `json:"id"`
	Version    uint64 `json:"version"`
	Kind       string `json:"kind"`
	Retention  string `json:"retention"`
	Content    string `json:"content"`
	TaskKey    string `json:"task_key"`
	EvidenceID string `json:"evidence_id"`
	Quote      string `json:"quote"`
	ValidUntil string `json:"valid_until"`
}

func knowledgeFingerprint(d document) string {
	b, _ := json.Marshal(struct {
		Entries    []Entry
		Settings   Settings
		Tombstones []tombstone
	}{latest(d), d.Settings, d.Tombstones})
	v := sha256.Sum256(b)
	return hex.EncodeToString(v[:])
}

func (s *Store) scopesOnDisk() []Scope {
	seen := map[Scope]bool{}
	for scope := range s.scopes {
		seen[scope] = true
	}
	files, _ := filepath.Glob(filepath.Join(s.root, "projects", "*.json"))
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var d document
		if json.Unmarshal(b, &d) == nil && d.Scope.valid() && s.path(d.Scope) == file {
			seen[d.Scope] = true
		}
	}
	out := make([]Scope, 0, len(seen))
	for scope := range seen {
		if scope.Kind == "project" {
			out = append(out, scope)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProjectKey < out[j].ProjectKey })
	return out
}

func jobRelated(s *Store, scope Scope, job ExtractionJob) []Entry {
	var text strings.Builder
	for _, e := range job.Evidence {
		if e.Kind == "user_statement" {
			text.WriteString(e.Text)
			text.WriteByte('\n')
		}
	}
	entries, _ := s.search(scope, text.String(), job.TaskKey, 5)
	var out []Entry
	for _, e := range entries {
		if e.Scope == scope {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) EnqueueExtraction(scope Scope, job ExtractionJob) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(scope)
	if err != nil {
		return false, err
	}
	if d.Settings.Disabled || !d.Settings.AutoExtract || d.DisabledSessions[job.Session] {
		return false, nil
	}
	if job.Session == "" || job.TurnID == "" || len(job.Evidence) == 0 {
		return false, errors.New("Memory extraction is missing its source turn")
	}
	sum := sha256.Sum256([]byte(job.Session + "\x00" + job.TurnID))
	job.Key = hex.EncodeToString(sum[:])
	for _, old := range d.Jobs {
		if old.Key == job.Key {
			return false, nil
		}
	}
	job.State, job.CreatedAt = "pending", s.now()
	count := 0
	for _, sc := range s.scopesOnDisk() {
		doc, err := s.load(sc)
		if err != nil {
			continue
		}
		for _, j := range doc.Jobs {
			if j.State == "pending" || j.State == "running" {
				count++
			}
		}
	}
	if count >= 100 {
		return s.skipExtraction(d, job, "Memory extraction queue is full. Skipped this turn.")
	}
	job.Candidates = jobRelated(s, scope, job)
	for _, e := range job.Candidates {
		job.Related = append(job.Related, stampOf(e))
	}
	job.Candidates = nil
	// The adapter budgets the evidence before the queue adds its identity and
	// timestamps. Keep user evidence and the final answer; optional tool results
	// must not make an otherwise valid completed round disappear at this boundary.
	for {
		b, _ := json.Marshal(job)
		if utf8.RuneCount(b) <= MaxExtractionInput-512 {
			break
		}
		tool := -1
		for i := len(job.Evidence) - 1; i >= 0; i-- {
			if job.Evidence[i].Kind == "tool_result" {
				tool = i
				break
			}
		}
		if tool < 0 {
			return s.skipExtraction(d, job, "Extraction input exceeds the limit. Skipped this turn.")
		}
		job.Evidence = append(append([]Evidence(nil), job.Evidence[:tool]...), job.Evidence[tool+1:]...)
	}
	return true, s.appendExtraction(d, job)
}

// Skipped rounds retain only bounded diagnostic metadata, never their input.
func (s *Store) skipExtraction(d document, job ExtractionJob, reason string) (bool, error) {
	finishJob(&job, "failed", reason, s.now())
	if err := s.appendExtraction(d, job); err != nil {
		return false, err
	}
	return false, errors.New(reason)
}

func (s *Store) appendExtraction(d document, job ExtractionJob) error {
	d.Jobs = append(d.Jobs, job)
	// Keep at most 100 terminal records per scope, without retaining evidence.
	terminal := 0
	kept := make([]ExtractionJob, 0, len(d.Jobs))
	for i := len(d.Jobs) - 1; i >= 0; i-- {
		j := d.Jobs[i]
		if j.State != "pending" && j.State != "running" {
			terminal++
			if terminal > 100 || s.now().Sub(j.CreatedAt) > 30*24*time.Hour {
				continue
			}
		}
		kept = append(kept, j)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	d.Jobs = kept
	return s.commit(d)
}

func finishJob(j *ExtractionJob, state, message string, now time.Time) {
	j.State, j.Error, j.CompletedAt = state, message, now
	j.Evidence, j.Candidates = nil, nil
}

func (s *Store) RecoverExtractions() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, scope := range s.scopesOnDisk() {
		d, err := s.load(scope)
		if err != nil {
			continue
		}
		changed := false
		for i := range d.Jobs {
			j := &d.Jobs[i]
			if j.State == "running" {
				changed = true
				if j.Attempts >= 2 {
					finishJob(j, "failed", "Extraction interrupted; no retries remaining", s.now())
				} else {
					j.State = "pending"
				}
			}
		}
		if changed {
			if err = s.commit(d); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ClaimExtraction() (Scope, ExtractionJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, scope := range s.scopesOnDisk() {
		d, err := s.load(scope)
		if err != nil {
			continue
		}
		for i := range d.Jobs {
			j := &d.Jobs[i]
			if j.State != "pending" {
				continue
			}
			if d.Settings.Disabled || !d.Settings.AutoExtract || d.DisabledSessions[j.Session] {
				finishJob(j, "canceled", "Automatic extraction is disabled for this project", s.now())
				if err = s.commit(d); err != nil {
					return Scope{}, ExtractionJob{}, false, err
				}
				continue
			}
			j.ExtractorAgent = d.Settings.ExtractionAgent()
			j.Candidates = jobRelated(s, scope, *j)
			j.Fingerprint = knowledgeFingerprint(d)
			j.State = "running"
			j.Attempts++
			// Candidate context is optional; never trim user evidence to fit it.
			fits := false
			for {
				b, _ := json.Marshal(j)
				if fits = utf8.RuneCount(b) <= MaxExtractionInput; fits || len(j.Candidates) == 0 {
					break
				}
				j.Candidates = j.Candidates[:len(j.Candidates)-1]
			}
			// Retrying cannot shrink the input; report the real reason instead of
			// spending attempts on a call that is rejected before it starts.
			if !fits {
				finishJob(j, "failed", "Extraction input exceeds the limit. Skipped this turn.", s.now())
				if err = s.commit(d); err != nil {
					return Scope{}, ExtractionJob{}, false, err
				}
				continue
			}
			if err = s.commit(d); err != nil {
				return Scope{}, ExtractionJob{}, false, err
			}
			return scope, *j, true, nil
		}
	}
	return Scope{}, ExtractionJob{}, false, nil
}

func sameSource(a, b Source) bool {
	if a.Thread == "" || a.Thread != b.Thread {
		return false
	}
	if a.LocalTurnID != "" && a.LocalTurnID == b.LocalTurnID {
		return true
	}
	return a.MessageID != "" && a.MessageID == b.MessageID || a.TurnID != "" && a.TurnID == b.TurnID
}

func scrubForgottenJobs(d *document, id string, sources []Source, now time.Time) {
	for i := range d.Jobs {
		j := &d.Jobs[i]
		hit := false
		for _, v := range j.Related {
			if v.ID == id {
				hit = true
			}
		}
		for _, e := range j.Candidates {
			if e.ID == id {
				hit = true
			}
		}
		for _, e := range j.Evidence {
			for _, src := range sources {
				if sameSource(e.Source, src) {
					hit = true
				}
			}
		}
		if hit {
			finishJob(j, "canceled", "The referenced memory was forgotten", now)
		}
	}
}

func deletionRequest(text string) bool {
	lower := strings.ToLower(text)
	lower = strings.TrimSpace(lower)
	for _, word := range []string{"忘记", "请忘记", "帮我忘记", "不要再记", "删除这条记忆", "forget", "please forget", "remove this memory", "delete this memory"} {
		if strings.HasPrefix(lower, word) {
			return true
		}
	}
	return false
}

func validateCandidate(d document, job ExtractionJob, c Candidate) (Mutation, string, error) {
	var ev Evidence
	for _, e := range job.Evidence {
		if e.ID == c.EvidenceID {
			ev = e
			break
		}
	}
	if ev.ID == "" || strings.TrimSpace(c.Quote) == "" || !strings.Contains(ev.Text, c.Quote) {
		return Mutation{}, "", errors.New("Extraction evidence does not belong to this turn")
	}
	if ev.Kind != "user_statement" && ev.Kind != "tool_result" && ev.Kind != "assistant_report" {
		return Mutation{}, "", errors.New("Invalid evidence category")
	}
	if c.Action != "forget" {
		if ev.Kind == "assistant_report" || c.Kind == "progress" {
			return Mutation{}, "", errors.New("Automatic memory does not retain assistant reports or task logs. Save them explicitly if needed.")
		}
		if c.Kind == "preference" && c.Retention != "preference" || c.Kind == "fact" && c.Retention != "decision" && c.Retention != "lesson" {
			return Mutation{}, "", errors.New("Automatic memory requires an explicit long-term retention category")
		}
		if c.Retention == "decision" && ev.Kind != "user_statement" {
			return Mutation{}, "", errors.New("Project decisions require explicit user confirmation")
		}
	}
	if c.Kind == "preference" && ev.Kind != "user_statement" {
		return Mutation{}, "", errors.New("Preferences must come from a user statement")
	}
	if c.Action != "add" {
		offered := false
		for _, e := range job.Candidates {
			if e.ID == c.ID && e.Version == c.Version {
				offered = true
			}
		}
		if !offered {
			return Mutation{}, "", errors.New("The modification target is not in the candidate list")
		}
		i, err := target(d, c.ID, c.Version)
		if err != nil {
			return Mutation{}, "", err
		}
		_ = i
	} else if c.ID != "" || c.Version != 0 {
		return Mutation{}, "", errors.New("New entries cannot specify a target")
	}
	if c.Action == "forget" && (ev.Kind != "user_statement" || !deletionRequest(c.Quote)) {
		return Mutation{}, "", errors.New("Forgetting requires an explicit user request")
	}
	source := ev.Source
	source.Quote, source.EvidenceKind = c.Quote, ev.Kind
	for _, mark := range d.Tombstones {
		for _, old := range mark.Sources {
			if sameSource(source, old) {
				return Mutation{}, "", errors.New("Forgotten sources cannot be restored automatically")
			}
		}
		sum := sha256.Sum256([]byte(c.Content))
		digest := hex.EncodeToString(sum[:])
		for _, v := range mark.Digests {
			if v == digest {
				return Mutation{}, "", errors.New("Forgotten content cannot be restored automatically")
			}
		}
	}
	m := Mutation{ID: c.ID, Version: c.Version, Kind: c.Kind, Content: strings.TrimSpace(c.Content), TaskKey: c.TaskKey, Sources: []Source{source}, ObservedAt: source.ObservedAt}
	if c.ValidUntil != "" {
		until, err := time.Parse(time.RFC3339, c.ValidUntil)
		if err != nil {
			return Mutation{}, "", err
		}
		m.ValidUntil = until
	}
	if c.Kind == "progress" && (job.TaskKey == "" || c.TaskKey != job.TaskKey) {
		return Mutation{}, "", errors.New("Project progress task identity mismatch")
	}
	if c.Action != "forget" {
		if err := validate(m, d.Scope); err != nil {
			return Mutation{}, "", err
		}
	}
	return m, ev.Kind, nil
}

// CompleteExtraction commits all accepted candidates and terminal job state
// atomically. Any intervening explicit mutation invalidates the whole result.
func (s *Store) CompleteExtraction(scope Scope, key string, candidates []Candidate, duration time.Duration, runError error, requeue bool) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(scope)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range d.Jobs {
		if d.Jobs[i].Key == key {
			index = i
			break
		}
	}
	if index < 0 || d.Jobs[index].State != "running" {
		return nil, nil
	}
	j := d.Jobs[index]
	j.DurationMs = duration.Milliseconds()
	if runError != nil {
		if requeue {
			j.State = "pending"
			j.Attempts--
		} else if j.Attempts < 2 {
			j.State = "pending"
			j.Error = "Extraction failed. Waiting to retry."
		} else {
			finishJob(&j, "failed", "Extraction failed. No further retries.", s.now())
		}
		d.Jobs[index] = j
		return nil, s.commit(d)
	}
	if d.Settings.Disabled || !d.Settings.AutoExtract || d.DisabledSessions[j.Session] || j.Fingerprint != knowledgeFingerprint(d) {
		finishJob(&j, "stale", "Memory or settings changed. Discarded the old result.", s.now())
		d.Jobs[index] = j
		return nil, s.commit(d)
	}
	var accepted []Entry
	added := false
	if len(candidates) > 3 {
		finishJob(&j, "failed", "Extraction returned more than three items", s.now())
		d.Jobs[index] = j
		return nil, s.commit(d)
	}
	for _, candidate := range candidates {
		if candidate.Action == "add" && added {
			j.Rejected++
			continue
		}
		m, _, err := validateCandidate(d, j, candidate)
		if err != nil {
			j.Rejected++
			continue
		}
		if candidate.Action == "forget" {
			s.searchIndex = nil
			i, _ := target(d, m.ID, m.Version)
			old := d.Entries[i]
			mark := tombstone{ID: old.ID, Version: old.Version, Revision: d.Revision + 1}
			kept := make([]Entry, 0, len(d.Entries))
			for _, e := range d.Entries {
				if e.ID != old.ID {
					kept = append(kept, e)
					continue
				}
				sum := sha256.Sum256([]byte(e.Content))
				mark.Digests = append(mark.Digests, hex.EncodeToString(sum[:]))
				for _, src := range e.Sources {
					src.Quote = ""
					mark.Sources = append(mark.Sources, src)
				}
			}
			e := Entry{ID: old.ID, Version: old.Version + 1, Scope: scope, State: "forgotten", CreatedAt: old.CreatedAt, UpdatedAt: s.now()}
			d.Entries = append(kept, e)
			d.Tombstones = append(d.Tombstones, mark)
			scrubForgottenJobs(&d, old.ID, mark.Sources, s.now())
			accepted = append(accepted, e)
			continue
		}
		if candidate.Action != "add" && candidate.Action != "supersede" && candidate.Action != "conflict" {
			j.Rejected++
			continue
		}
		duplicate := false
		for _, e := range latest(d) {
			if e.State == "active" && normalized(e.Content) == normalized(m.Content) {
				duplicate = true
			}
		}
		if duplicate {
			continue
		}
		e := Entry{Scope: scope, Kind: m.Kind, Content: m.Content, TaskKey: m.TaskKey, Sources: m.Sources, ObservedAt: m.ObservedAt, ValidUntil: m.ValidUntil, State: "active", Version: 1, CreatedAt: s.now(), UpdatedAt: s.now()}
		if e.Kind == "progress" && e.ValidUntil.IsZero() {
			e.ValidUntil = e.ObservedAt.Add(7 * 24 * time.Hour)
		}
		if m.ID != "" {
			i, err := target(d, m.ID, m.Version)
			if err != nil {
				j.Rejected++
				continue
			}
			old := d.Entries[i]
			if candidate.Action == "conflict" {
				d.Entries[i].State = "superseded"
				conflict := old
				conflict.Version++
				conflict.Supersedes = old.Version
				conflict.State = "conflicted"
				conflict.UpdatedAt = s.now()
				d.Entries = append(d.Entries, conflict)
				e.State = "conflicted"
			} else {
				d.Entries[i].State = "superseded"
				e.ID, e.Version, e.Supersedes, e.CreatedAt = old.ID, old.Version+1, old.Version, old.CreatedAt
				e.Sources = append(append([]Source(nil), old.Sources...), m.Sources...)
				if len(e.Sources) > 8 {
					e.Sources = e.Sources[len(e.Sources)-8:]
				}
			}
		}
		if e.ID == "" {
			e.ID, err = newID()
			if err != nil {
				return nil, err
			}
		}
		d.Entries = append(d.Entries, e)
		accepted = append(accepted, e)
		if candidate.Action == "add" {
			added = true
		}
	}
	j.Accepted = len(accepted)
	finishJob(&j, "completed", "", s.now())
	d.Jobs[index] = j
	if err = s.commit(d); err != nil {
		return nil, err
	}
	return accepted, nil
}

type ExtractionStatus struct {
	Pending        int    `json:"pending"`
	Completed      int    `json:"completed"`
	Failed         int    `json:"failed"`
	Rejected       int    `json:"rejected"`
	Accepted       int    `json:"accepted"`
	LastDurationMs int64  `json:"lastDurationMs"`
	LastError      string `json:"lastError,omitempty"`
}

func (s *Store) ExtractionStatus(scope Scope) (ExtractionStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(scope)
	if err != nil {
		return ExtractionStatus{}, err
	}
	var out ExtractionStatus
	// Only a current problem is reported: a job waiting to retry, or a most
	// recent terminal job that failed. Later successes clear older failures, and
	// user cancellation or stale results are normal outcomes, not problems.
	var retryError string
	var last *ExtractionJob
	for i := range d.Jobs {
		j := &d.Jobs[i]
		switch j.State {
		case "pending", "running":
			out.Pending++
			if j.Error != "" {
				retryError = j.Error
			}
		case "completed":
			out.Completed++
		default:
			out.Failed++
		}
		if j.State != "pending" && j.State != "running" && (last == nil || !j.CompletedAt.Before(last.CompletedAt)) {
			last = j
		}
		out.Accepted += j.Accepted
		out.Rejected += j.Rejected
		if j.DurationMs > 0 {
			out.LastDurationMs = j.DurationMs
		}
	}
	if retryError != "" {
		out.LastError = retryError
	} else if last != nil && last.State == "failed" {
		out.LastError = last.Error
	}
	return out, nil
}

func (j ExtractionJob) InputJSON() ([]byte, error) {
	b, err := json.Marshal(j)
	if err == nil && utf8.RuneCount(b) > MaxExtractionInput {
		return nil, fmt.Errorf("Extraction input exceeds %d characters", MaxExtractionInput)
	}
	return b, err
}

func (s *Store) ConfigureSession(scope Scope, session string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(scope)
	if err != nil {
		return err
	}
	if d.DisabledSessions == nil {
		d.DisabledSessions = map[string]bool{}
	}
	if disabled {
		d.DisabledSessions[session] = true
		delete(d.Sessions, session)
		for i := range d.Jobs {
			if d.Jobs[i].Session == session && (d.Jobs[i].State == "pending" || d.Jobs[i].State == "running") {
				finishJob(&d.Jobs[i], "canceled", "Automatic memory is disabled for this session", s.now())
			}
		}
	} else {
		delete(d.DisabledSessions, session)
	}
	return s.commit(d)
}
