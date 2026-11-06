package protocol

type MemoryArgs struct {
	Query           string `json:"query,omitempty"`
	ID              string `json:"id,omitempty"`
	Version         uint64 `json:"version,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Content         string `json:"content,omitempty"`
	TaskKey         string `json:"task_key,omitempty"`
	ValidUntil      string `json:"valid_until,omitempty"`
	SourceMessageID string `json:"source_message_id,omitempty"`
	Quote           string `json:"quote,omitempty"`
}
