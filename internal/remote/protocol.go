package remote

import "encoding/json"

// ProtocolVersion is bumped whenever a frame's meaning changes. The phone
// refuses a desktop whose version it does not know.
const ProtocolVersion = 4

// Frame is one line from the desktop to the phone.
//
//	hello   the first frame: Version and Host
//	state   Data is the desktop's projects, its conversations without
//	        messages, and the approvals waiting for an answer
//	thread  Data brings the phone's copy of Thread up to date: its
//	        metadata, the order of its messages and those that changed.
//	        Every frame after hello is compressed (Conn.CompressSends).
//	commands Data is a project's Claude Code slash commands
//	image   Data is a requested attachment thumbnail
//	sent    answers a send: Thread took the message, or Error says why not
//	ping    keeps an idle connection checked
type Frame struct {
	Type    string          `json:"type"`
	Version int             `json:"version,omitempty"`
	Host    string          `json:"host,omitempty"`
	Thread  int             `json:"thread,omitempty"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Request is one line from the phone to the desktop.
//
//	subscribe  send Thread's messages, 0 for none; Have lists the
//	           messages the phone holds already
//	send       send Text into Thread, or start a conversation of Agent in
//	           Project when Thread is 0
//	stop       stop Thread's running turn
//	commands   send the Claude Code commands of Project
//	respond    answer the approval whose ID is Approval, allowing it or not
//
// SendOptions are explicit per-message settings chosen on the phone.
// A nil Options pointer follows desktop defaults; empty values within an
// explicit choice ask the agent to use its own default.
type SendOptions struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
	Policy string `json:"policy"`
}

type Request struct {
	ImageSource     string          `json:"imageSource,omitempty"`
	ApprovalVersion string          `json:"approvalVersion,omitempty"`
	Options         *SendOptions    `json:"options,omitempty"`
	Steer           bool            `json:"steer,omitempty"`
	QueuedID        int             `json:"queuedId,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Remember        bool            `json:"remember,omitempty"`
	ReadVersion     uint64          `json:"readVersion,omitempty"`
	Push            json.RawMessage `json:"push,omitempty"`
	Op              string          `json:"op"`
	Thread          int             `json:"thread"`
	// ServerID is the durable identity; Thread is a desktop-local number.
	// Optional for compatibility with phones that only send Thread.
	ServerID string          `json:"serverId,omitempty"`
	Project  string          `json:"project,omitempty"`
	Agent    string          `json:"agent,omitempty"`
	Text     string          `json:"text,omitempty"`
	Approval json.RawMessage `json:"approval,omitempty"`
	Allow    bool            `json:"allow,omitempty"`
	// Images go with a send, as attachments of the message.
	Images []Image `json:"images,omitempty"`
	// Have is what the phone holds of Thread as it subscribes.
	Have []Seen `json:"have,omitempty"`
}

// Seen is a message the phone holds: its ID and its fingerprint.
type Seen struct {
	ID int    `json:"id"`
	FP uint64 `json:"fp"`
}

// Image is a picture a phone attaches to a message.
type Image struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}
