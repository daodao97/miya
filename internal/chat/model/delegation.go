package model

// DelegationNoticeHeading identifies the continuation prompt preserved in native histories.
const DelegationNoticeHeading = "[Miya collaboration] These delegated tasks finished."

// DelegationCard describes one delegated execution in the conversation timeline.
type DelegationCard struct {
	TaskID  string `json:"taskId,omitempty"`
	Title   string `json:"title"`
	Agent   string `json:"agent,omitempty"`
	Thread  string `json:"thread,omitempty"`
	State   string `json:"state,omitempty"`
	Request string `json:"request,omitempty"`
	Quote   string `json:"quote,omitempty"`
	Blocked string `json:"blocked,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Expired bool   `json:"expired,omitempty"`
	// Read marks a receipt whose result the parent then fetched with
	// read_task, and that read_task call, which the timeline folds away.
	Read bool `json:"read,omitempty"`
}
