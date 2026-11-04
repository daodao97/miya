// Package delegation records cross-agent requests independently of the sessions
// that execute them. Native execution and caller authentication stay in adapters.
package delegation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type State string

const (
	Queued         State = "queued"
	Starting       State = "starting"
	Running        State = "running"
	Completed      State = "completed"
	Failed         State = "failed"
	Interrupted    State = "interrupted"
	NeedsAttention State = "needsAttention"
	ResultLimit          = 256 << 10
)

func Terminal(s State) bool { return s == Completed || s == Failed || s == Interrupted }
func Occupies(s State) bool { return s == Starting || s == Running || s == NeedsAttention }

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string           { return e.Code + ": " + e.Message }
func errCode(code, message string) error { return &Error{code, message} }
func IsCode(err error, code string) bool { var e *Error; return errors.As(err, &e) && e.Code == code }

type Config struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Policy string `json:"policy,omitempty"`
}

type StartInput struct {
	From           string `json:"from"`
	RequestID      string `json:"request_id"`
	Target         string `json:"target,omitempty"`
	ParentTaskID   string `json:"parent_task_id,omitempty"`
	PreviousTaskID string `json:"previous_task_id,omitempty"`
	Agent          string `json:"agent"`
	Workspace      string `json:"workspace"`
	Title          string `json:"title"`
	Prompt         string `json:"task"`
	Context        string `json:"context,omitempty"`
	Snapshot       string `json:"snapshot,omitempty"`
	Requested      Config `json:"requested"`
	Effective      Config `json:"effective"`
	Notify         bool   `json:"notify"`
}

type Result struct {
	Availability string   `json:"availability"`
	Source       string   `json:"source,omitempty"`
	Text         string   `json:"text,omitempty"`
	Artifacts    []string `json:"artifacts,omitempty"`
	Error        string   `json:"error,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
}

type Task struct {
	ID               string    `json:"task_id"`
	RequestID        string    `json:"request_id"`
	RequestHash      string    `json:"request_hash"`
	From             string    `json:"from"`
	Target           string    `json:"thread,omitempty"`
	NewSession       bool      `json:"new_session"`
	ParentTaskID     string    `json:"parent_task_id,omitempty"`
	PreviousTaskID   string    `json:"previous_task_id,omitempty"`
	Depth            int       `json:"depth"`
	Agent            string    `json:"agent"`
	Workspace        string    `json:"workspace"`
	Title            string    `json:"title"`
	Prompt           string    `json:"task,omitempty"`
	Snapshot         string    `json:"snapshot,omitempty"`
	Config           Config    `json:"configuration"`
	Requested        Config    `json:"requested_configuration,omitempty"`
	State            State     `json:"state"`
	WaitCause        string    `json:"wait_cause,omitempty"`
	CancelRequested  bool      `json:"cancel_requested,omitempty"`
	Resolution       string    `json:"resolution,omitempty"`
	ResolutionReason string    `json:"resolution_reason,omitempty"`
	NativeTurnID     string    `json:"native_turn_id,omitempty"`
	SessionEpoch     string    `json:"session_epoch,omitempty"`
	SubmissionPhase  string    `json:"submission_phase,omitempty"`
	Progress         string    `json:"progress,omitempty"`
	RecheckError     string    `json:"recheck_error,omitempty"`
	Result           Result    `json:"result"`
	Revision         uint64    `json:"revision"`
	LastSequence     uint64    `json:"last_sequence,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
}

type Event struct {
	TaskID           string
	Target           string
	NativeTurnID     string
	NativeResultID   string // Claude root result UUID, when provided by the native stream
	SessionEpoch     string
	SubmissionPhase  string `json:",omitempty"`
	State            State
	WaitCause        string
	Progress         string
	Result           Result
	Sequence         uint64
	ExpectedRevision uint64
}

type Watch struct {
	TaskID string `json:"task_id"`
	Notify string `json:"notify"`
	State  string `json:"state"`
}

type Delivery struct {
	ID               string    `json:"delivery_id"`
	TaskIDs          []string  `json:"task_ids"`
	Notify           string    `json:"notify"`
	State            string    `json:"state"`
	TargetTurnID     string    `json:"target_turn_id,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	Attempts         int       `json:"attempts,omitempty"`
	NextAttempt      time.Time `json:"next_attempt,omitempty"`
	SubmissionPhase  string    `json:"submission_phase,omitempty"`
	Resolution       string    `json:"resolution,omitempty"`
	ResolutionReason string    `json:"resolution_reason,omitempty"`
}

// Operation receipts survive later state changes. Retrying an old watch must
// not reactivate it after a newer unwatch has committed.
type Operation struct {
	Caller     string `json:"caller"`
	RequestID  string `json:"request_id"`
	Name       string `json:"operation"`
	TaskID     string `json:"task_id"`
	Reason     string `json:"reason,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
}

type Receipt struct {
	Operation
	Hash         string    `json:"hash"`
	Accepted     bool      `json:"accepted"`
	Notification string    `json:"notification,omitempty"`
	AcceptedAt   time.Time `json:"accepted_at"`
	TaskIDs      []string  `json:"task_ids,omitempty"` // fixed accepted scope for cancel_all
}

type Snapshot struct {
	Revision                 uint64              `json:"revision"`
	Tasks                    map[string]Task     `json:"tasks"`
	Parents                  map[string]string   `json:"parents"`
	Watches                  map[string]Watch    `json:"watches"`
	Deliveries               map[string]Delivery `json:"deliveries"`
	Paused                   map[string]bool     `json:"paused"`
	Operations               map[string]Receipt  `json:"operations"`
	queued, occupied, active int                 // derived only in the immutable published view
}

func emptySnapshot() Snapshot {
	return Snapshot{Tasks: map[string]Task{}, Parents: map[string]string{}, Watches: map[string]Watch{}, Deliveries: map[string]Delivery{}, Paused: map[string]bool{}, Operations: map[string]Receipt{}}
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("delegation identity: %v", err))
	}
	return prefix + hex.EncodeToString(b[:])
}

func bounded(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimSpace(s), true
}
