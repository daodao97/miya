package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sync"
	"time"

	"miya/internal/codex"
)

// Miya runs in two processes, as Retty does. The service (the executable
// run with -server) owns the Codex app-server, every Claude Code run and the
// conversations; windows connect to it over a Unix socket and only show its
// state, so quitting the app leaves running turns running.
//
// The socket carries JSON lines. A window sends requests (op and args, an
// id when it waits for the result); the service answers with the same id
// and pushes events: the state without messages, the messages of the
// conversation a window subscribed to, and drafts that could not be sent.

const serviceVersion = 2

type svcRequest struct {
	ID   int64           `json:"id,omitempty"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

type svcMessage struct {
	ID       int64           `json:"id,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Event    string          `json:"event,omitempty"`
	State    *svcState       `json:"state,omitempty"`
	Messages *svcMessages    `json:"messages,omitempty"`
	Draft    *svcDraft       `json:"draft,omitempty"`
}

// svcState is everything a window shows but the messages.
type svcState struct {
	Collaboration      *collaborationStatus `json:"collaboration,omitempty"`
	MemoryRevision     uint64               `json:"memoryRevision,omitempty"`
	ReferenceSource    string               `json:"referenceSource"`
	FastMode           bool                 `json:"fastMode"`
	Cwd                string               `json:"cwd"`
	Projects           []Project            `json:"projects"`
	ProjectBusy        map[string]bool      `json:"projectBusy,omitempty"`
	Threads            []Thread             `json:"threads"`
	Approvals          []codex.Approval     `json:"approvals"`
	Status             string               `json:"status"`
	HistoryStatus      string               `json:"historyStatus"`
	Ready              bool                 `json:"ready"`
	Models             []codex.Model        `json:"models"`
	Model              string               `json:"model"`
	Effort             string               `json:"effort"`
	PermissionPolicy   string               `json:"permissionPolicy"`
	Locale             string               `json:"locale"`
	ClaudeCommand      string               `json:"claudeCommand"`
	ClaudeError        string               `json:"claudeError"`
	ClaudeBinary       string               `json:"claudeBinary"`
	ClaudeArgs         []string             `json:"claudeArgs"`
	ClaudeEnv          []string             `json:"claudeEnv"`
	ClaudeModel        string               `json:"claudeModel"`
	ClaudeEffort       string               `json:"claudeEffort"`
	ClaudeDefaultModel string               `json:"claudeDefaultModel"`
	Account            accountDisplay       `json:"account"`
	AccountReset       accountResetAction   `json:"accountReset"`
	QuotaLimits        accountLimits        `json:"quotaLimits"`
	QuotaStatus        string               `json:"quotaStatus"`
	Phone              phoneStatus          `json:"phone"`

	ProjectThreads map[string][]Thread `json:"projectThreads,omitempty"`
	DesktopUnread  map[string]int64    `json:"desktopUnread,omitempty"`
}

// svcMessages updates a conversation's messages: Order lists them all by
// ID, Changed has those the window does not have as they are now. Reset
// drops what the window had first.
type svcMessages struct {
	Thread  int       `json:"thread"`
	Order   []int     `json:"order"`
	Changed []Message `json:"changed,omitempty"`
	Reset   bool      `json:"reset,omitempty"`
}

type svcDraft struct {
	Thread      int      `json:"thread"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
}

type svcHello struct {
	Remote  bool      `json:"remote,omitempty"`
	Version int       `json:"version"`
	Exe     string    `json:"exe"`
	ExeTime time.Time `json:"exeTime"`
	PID     int       `json:"pid"`
	Busy    bool      `json:"busy"`
}

type svcThreadValue struct {
	Thread int    `json:"thread"`
	Value  string `json:"value"`
}

type svcTaskAction struct {
	Thread       int
	Task, Action string
}

type svcRespond struct {
	Remember bool            `json:"remember,omitempty"`
	ID       json.RawMessage `json:"id"`
	Result   any             `json:"result"`
}

type svcQueued struct {
	Thread int `json:"thread"`
	ID     int `json:"id"`
}

type svcRewind struct {
	Thread  int    `json:"thread"`
	Message int    `json:"message"`
	Text    string `json:"text"`
}

type svcCommand struct {
	Thread  int    `json:"thread"`
	Agent   string `json:"agent"`
	Command string `json:"command"`
	Policy  string `json:"policy"`
}

type svcCommandResult struct {
	FastMode bool   `json:"fastMode"`
	Thread   int    `json:"thread"`
	Message  string `json:"message"`
}

type svcProject struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

type svcWorkspace struct {
	Cwd   string `json:"cwd,omitempty"`
	Error string `json:"error,omitempty"`
}

// serviceDir keeps the service's lock, socket and log.
func serviceDir() string {
	if d := os.Getenv("MIYA_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "Miya")
}

// serviceSocket is in serviceDir unless that path is too long for a socket.
func serviceSocket() string {
	p := filepath.Join(serviceDir(), "service.sock")
	if len(p) < 100 {
		return p
	}
	h := fnv.New32a()
	h.Write([]byte(p))
	return filepath.Join(os.TempDir(), fmt.Sprintf("miya-%d-%x.sock", os.Getuid(), h.Sum32()))
}

// messageFingerprint changes whenever what a window shows of a message does,
// cheaply enough to compute for a whole conversation on every update.
func messageFingerprint(m Message) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%d|%d|%d|%d|%d|", m.Role, m.Kind, m.Status, m.ItemID, m.TurnID, len(m.Content), m.SentAtMs, m.StartedAtMs, m.CompletedAtMs, len(m.Images))
	head, tail := m.Content, m.Content
	if len(head) > 64 {
		head, tail = head[:64], tail[len(tail)-64:]
	}
	h.Write([]byte(head))
	h.Write([]byte(tail))
	h.Write([]byte(m.HandoffTitle))
	if m.Memory != nil {
		b, _ := json.Marshal(m.Memory)
		h.Write(b)
	}
	for _, image := range m.Images {
		fmt.Fprintf(h, "|%d:", len(image))
		h.Write([]byte(image))
	}
	if len(m.Parts) > 0 {
		data, _ := json.Marshal(m.Parts)
		h.Write(data)
	}
	for _, p := range m.Plan {
		h.Write([]byte(p.Status + "|" + p.Step))
	}
	for _, a := range m.Agents {
		fmt.Fprintf(h, "|%s|%s|%s|%s|%d", a.ID, a.Name, a.Status, a.Detail, a.Steps)
	}
	if m.Item != nil {
		fmt.Fprintf(h, "|%d", len(m.Item.Changes))
	}
	return h.Sum64()
}

// executor runs functions one at a time, in order, on the goroutine that
// loops it: the service's main thread.
type executor struct {
	mu   sync.Mutex
	q    []func()
	wake chan struct{}
}

func newExecutor() *executor { return &executor{wake: make(chan struct{}, 1)} }

func (e *executor) push(f func()) {
	e.mu.Lock()
	e.q = append(e.q, f)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *executor) loop(quit <-chan struct{}) {
	for {
		e.mu.Lock()
		q := e.q
		e.q = nil
		e.mu.Unlock()
		for _, f := range q {
			f()
		}
		if len(q) > 0 {
			continue
		}
		select {
		case <-e.wake:
		case <-quit:
			return
		}
	}
}

// do runs f on the executor and waits for it.
func (e *executor) do(f func()) {
	done := make(chan struct{})
	e.push(func() { f(); close(done) })
	<-done
}
