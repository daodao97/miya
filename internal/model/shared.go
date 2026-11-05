package model

// Data the desktop, its service and the phone pass between them.

// Project is a workspace directory and the name it shows.
type Project struct {
	Name string
	Path string
}

// Only capability and revision metadata travels with chat state. Task bodies,
// context snapshots and results are fetched explicitly, never broadcast.
type CollaborationStatus struct {
	Version      int      `json:"version"`
	Revision     uint64   `json:"revision"`
	Operations   []string `json:"operations"`
	StorageError string   `json:"storage_error,omitempty"`
	Stale        bool     `json:"stale,omitempty"`
	CleanupError string   `json:"cleanup_error,omitempty"`
}

// ComposerChoice is a completion the composer offers: a command, a
// conversation, an agent or a file.
type ComposerChoice struct {
	Name, Detail string
	ArgumentHint string     `json:"argumentHint,omitempty"`
	Ref          *ThreadRef `json:"ref,omitempty"`
	Agent        string     `json:"agent,omitempty"`
}
