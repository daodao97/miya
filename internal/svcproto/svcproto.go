// Package svcproto is what a window and the background service say over the
// service socket: one JSON Request per line from the window, one Envelope per
// line back. Field names and JSON tags are the wire format between versions.
package svcproto

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"

	"miya/internal/agent/account"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appdir"
	"miya/internal/chat/model"
)

const Version = 2

type Request struct {
	ID   int64           `json:"id,omitempty"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

type Envelope struct {
	ID       int64           `json:"id,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Event    string          `json:"event,omitempty"`
	State    *State          `json:"state,omitempty"`
	Messages *ThreadMessages `json:"messages,omitempty"`
	Draft    *Draft          `json:"draft,omitempty"`
}

// State is everything a window shows but the messages.
type State struct {
	Collaboration      *model.CollaborationStatus `json:"collaboration,omitempty"`
	MemoryRevision     uint64                     `json:"memoryRevision,omitempty"`
	ReferenceSource    string                     `json:"referenceSource"`
	FastMode           bool                       `json:"fastMode"`
	Cwd                string                     `json:"cwd"`
	Projects           []model.Project            `json:"projects"`
	ProjectBusy        map[string]bool            `json:"projectBusy,omitempty"`
	Threads            []model.Thread             `json:"threads"`
	Approvals          []codex.Approval           `json:"approvals"`
	Status             string                     `json:"status"`
	HistoryStatus      string                     `json:"historyStatus"`
	Ready              bool                       `json:"ready"`
	Models             []codex.Model              `json:"models"`
	Model              string                     `json:"model"`
	Effort             string                     `json:"effort"`
	PermissionPolicy   string                     `json:"permissionPolicy"`
	Locale             string                     `json:"locale"`
	ClaudeCommand      string                     `json:"claudeCommand"`
	ClaudeError        string                     `json:"claudeError"`
	ClaudeBinary       string                     `json:"claudeBinary"`
	ClaudeArgs         []string                   `json:"claudeArgs"`
	ClaudeEnv          []string                   `json:"claudeEnv"`
	ClaudeModel        string                     `json:"claudeModel"`
	ClaudeEffort       string                     `json:"claudeEffort"`
	ClaudeDefaultModel string                     `json:"claudeDefaultModel"`
	Account            account.Display            `json:"account"`
	AccountReset       account.ResetAction        `json:"accountReset"`
	QuotaLimits        account.Limits             `json:"quotaLimits"`
	QuotaStatus        string                     `json:"quotaStatus"`
	Phone              PhoneStatus                `json:"phone"`

	ProjectThreads map[string][]model.Thread `json:"projectThreads,omitempty"`
	DesktopUnread  map[string]int64          `json:"desktopUnread,omitempty"`
}

// ThreadMessages updates a conversation's messages: Order lists them all by
// ID, Changed has those the window does not have as they are now. Reset
// drops what the window had first.
type ThreadMessages struct {
	Thread  int             `json:"thread"`
	Order   []int           `json:"order"`
	Changed []model.Message `json:"changed,omitempty"`
	Reset   bool            `json:"reset,omitempty"`
}

type Draft struct {
	Thread      int      `json:"thread"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
}

type Hello struct {
	Remote  bool      `json:"remote,omitempty"`
	Version int       `json:"version"`
	Exe     string    `json:"exe"`
	ExeTime time.Time `json:"exeTime"`
	PID     int       `json:"pid"`
	Busy    bool      `json:"busy"`
}

type ThreadValue struct {
	Thread int    `json:"thread"`
	Value  string `json:"value"`
}

type TaskAction struct {
	Thread       int
	Task, Action string
}

type Respond struct {
	Remember bool            `json:"remember,omitempty"`
	ID       json.RawMessage `json:"id"`
	Result   any             `json:"result"`
}

type Queued struct {
	Thread int `json:"thread"`
	ID     int `json:"id"`
}

type Rewind struct {
	Thread  int    `json:"thread"`
	Message int    `json:"message"`
	Text    string `json:"text"`
}

type Command struct {
	Thread  int    `json:"thread"`
	Agent   string `json:"agent"`
	Command string `json:"command"`
	Policy  string `json:"policy"`
}

type CommandResult struct {
	FastMode bool   `json:"fastMode"`
	Thread   int    `json:"thread"`
	Message  string `json:"message"`
}

type ProjectRequest struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

type WorkspaceResult struct {
	Cwd   string `json:"cwd,omitempty"`
	Error string `json:"error,omitempty"`
}

// Socket is in appdir.Dir unless that path is too long for a socket.
func Socket() string {
	p := filepath.Join(appdir.Dir(), "service.sock")
	if len(p) < 100 {
		return p
	}
	h := fnv.New32a()
	h.Write([]byte(p))
	return filepath.Join(os.TempDir(), fmt.Sprintf("miya-%d-%x.sock", os.Getuid(), h.Sum32()))
}

// MessageFingerprint changes whenever what a window shows of a message does,
// cheaply enough to compute for a whole conversation on every update.
func MessageFingerprint(m model.Message) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%d|%d|%d|%d|%d|", m.Role, m.Kind, m.Status, m.ItemID, m.TurnID, len(m.Content), m.SentAtMs, m.StartedAtMs, m.CompletedAtMs, len(m.Images))
	head, tail := m.Content, m.Content
	if len(head) > 64 {
		head, tail = head[:64], tail[len(tail)-64:]
	}
	h.Write([]byte(head))
	h.Write([]byte(tail))
	h.Write([]byte(m.HandoffTitle))
	if m.Presentation != nil {
		b, _ := json.Marshal(m.Presentation)
		h.Write(b)
	}
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

// PhoneStatus is what the settings show of the phone connection; the
// service sends it to its windows.
type PhoneStatus struct {
	Enabled bool   `json:"enabled,omitempty"`
	Busy    bool   `json:"busy,omitempty"`
	Link    string `json:"link,omitempty"`
	Peers   int    `json:"peers,omitempty"`
	Message string `json:"message,omitempty"`
}
