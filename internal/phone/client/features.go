package client

import (
	"encoding/json"
	"time"

	"miya/internal/automation"
	"miya/internal/remote"
)

// ReceiveFeatureFrame runs on the client owner, behind connection-generation
// checks in read. Public for transports and deterministic UI tests.
func (m *Client) ReceiveFeatureFrame(f remote.Frame) {
	switch f.Type {
	case "memory":
		m.ReceiveMemory(f.Data, f.Error)
	case "automations":
		m.ReceiveAutomations(f.Data, f.Error)
	case "automation-run":
		m.ReceiveAutomationRun(f.Data, f.Error)
	case "automation-draft", "automation-create":
		m.ReceiveAutomationForm(f.Type, f.Data, f.Error)
	case "collaboration":
		m.ReceiveCollaboration(f.Data, f.Error)
	case "automation-webhook":
		var v automation.WebhookView
		if f.Error == "" && json.Unmarshal(f.Data, &v) == nil {
			m.Autos.Webhook = &v
		}
	}
}

// TickFeatures bounds reads and uncertain mutations even if a connection stays
// alive without replying. The adapter only ticks while a feature sheet is open.
func (m *Client) TickFeatures() {
	now := time.Now()
	a := &m.Autos
	f := &a.Form
	mem := &m.Memories
	if a.Loading && now.Sub(a.RequestedAt) > 10*time.Second {
		a.Loading = false
		a.Err = tr("Read timed out. Try again.")
		a.RequestedAt = now
	}
	if mem.Loading && now.Sub(mem.RequestedAt) > 10*time.Second {
		mem.Loading = false
		mem.Error = tr("Read timed out. Try again.")
		mem.RequestedAt = now
	}
	if a.RunRequest != "" && now.Sub(a.RunRequestedAt) > 30*time.Second {
		a.RunRequest = ""
		a.RunError = tr("Request timed out. Refresh to check run status.")
		a.RequestedAt = time.Time{}
	}
	limit := 150 * time.Second
	if f.Op == "automation-create" {
		limit = 30 * time.Second
	}
	if f.Pending != "" && now.Sub(f.RequestedAt) > limit {
		f.Pending = ""
		f.Error = tr("Request timed out. Try again.")
	}
}
