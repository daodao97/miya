package automation

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"miya/internal/notification"
	"miya/internal/textutil"
)

// WebhookView is Miya's shared inbound endpoint, including recent routing
// results. It exists before any automation has been created.
type WebhookView struct {
	Enabled  bool             `json:"enabled"`
	URL      string           `json:"url,omitempty"`
	Error    string           `json:"error,omitempty"`
	Messages []WebhookMessage `json:"messages,omitempty"`
}

type WebhookMessage struct {
	ID     string         `json:"id"`
	At     time.Time      `json:"at"`
	Data   string         `json:"data"`
	Dedupe string         `json:"dedupe,omitempty"`
	Routes []WebhookRoute `json:"routes,omitempty"`
	Detail string         `json:"detail,omitempty"`
}

type WebhookRoute struct {
	Automation string `json:"automation"`
	Name       string `json:"name"`
	Trigger    string `json:"trigger"`
	Outcome    string `json:"outcome"`
	Detail     string `json:"detail,omitempty"`
}

func (rn *Runner) WebhookView() WebhookView {
	st := rn.relayStates[globalRelayKey]
	return WebhookView{Enabled: !rn.Store.WebhookDisabled, URL: st.URL, Error: st.Error, Messages: append([]WebhookMessage(nil), rn.Store.Inbox...)}
}

func (v WebhookView) Status() string {
	if !v.Enabled {
		return tr("Off")
	}
	if v.Error != "" {
		return tr("Webhook unavailable: ") + v.Error
	}
	if v.URL != "" {
		return tr("Webhook ready")
	}
	return tr("Registering webhook…")
}

// ReceiveWebhook first records reception, then routes the untrusted data to
// matching subscriptions. No external sender needs a task ID. A message may
// match several automations; multiple matching triggers wake each only once.
// Optional routing metadata is namespaced under "miya" to leave event data
// untouched. Explicit destinations never fall back to other automations.
func (rn *Runner) ReceiveWebhook(data string) WebhookMessage {
	message := WebhookMessage{ID: NewID(), At: rn.Now(), Data: clipData(data)}
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&object)
	var target, trigger string
	if rawMeta, exists := object["miya"]; exists {
		meta, ok := rawMeta.(map[string]any)
		if !ok {
			message.Detail = "Invalid Miya routing metadata"
			return rn.keepWebhook(message)
		}
		for name, destination := range map[string]*string{"automation": &target, "trigger": &trigger, "dedupe": &message.Dedupe} {
			if value, exists := meta[name]; exists {
				text, ok := value.(string)
				if !ok {
					message.Detail = "Invalid Miya routing metadata"
					return rn.keepWebhook(message)
				}
				*destination = strings.TrimSpace(text)
				if name == "automation" && *destination == "" {
					message.Detail = "Invalid Miya routing metadata"
					return rn.keepWebhook(message)
				}
			}
		}
	}
	if message.Dedupe == "" {
		message.Dedupe, _ = object["dedupe"].(string)
	}
	message.Dedupe = textutil.ClipRunes(message.Dedupe, 300)
	if message.Dedupe != "" {
		for _, old := range rn.Store.Inbox {
			if old.Dedupe == message.Dedupe {
				message.Detail = "Already received"
				return rn.keepWebhook(message)
			}
		}
	}
	rn.keepWebhook(message)
	if rn.Store.WebhookDisabled {
		message.Detail = "The webhook receiver is off"
		return rn.keepWebhook(message)
	}
	if target != "" {
		if _, exactID := rn.Store.Find(target); !exactID {
			var candidates []string
			for _, a := range rn.Store.Automations {
				if strings.EqualFold(a.Name, target) {
					candidates = append(candidates, a.ID)
				}
			}
			if len(candidates) > 1 {
				message.Detail = "Automation name is ambiguous; use its ID"
				return rn.keepWebhook(message)
			}
			if len(candidates) == 1 {
				target = candidates[0]
			}
		}
	}
	for _, a := range rn.Store.Automations {
		if !a.Enabled || target != "" && a.ID != target {
			continue
		}
		for _, t := range a.Triggers {
			if t.Kind != TriggerIngress || t.Disabled || trigger != "" && trigger != t.ID || !matchesWebhook(t, object, data) {
				continue
			}
			wake, err := rn.Wake(WakeEvent{Automation: a.ID, Trigger: t.ID, Source: TriggerIngress, Reason: "Message received by Miya webhook", Data: data, Dedupe: message.Dedupe})
			detail := wake.Detail
			if err != nil && detail == "" {
				detail = err.Error()
			}
			message.Routes = append(message.Routes, WebhookRoute{a.ID, a.Name, t.ID, wake.Outcome, detail})
			break
		}
	}
	if len(message.Routes) == 0 {
		message.Detail = "No matching automation"
	}
	return rn.keepWebhook(message)
}

func matchesWebhook(t Trigger, object map[string]any, data string) bool {
	if t.Contains != "" && !strings.Contains(strings.ToLower(data), strings.ToLower(t.Contains)) {
		return false
	}
	for field, want := range t.Match {
		var value any = object
		for _, part := range strings.Split(field, ".") {
			next, ok := value.(map[string]any)
			if !ok {
				return false
			}
			value, ok = next[part]
			if !ok {
				return false
			}
		}
		switch value.(type) {
		case string, json.Number, bool:
		default:
			return false
		}
		if fmt.Sprint(value) != want {
			return false
		}
	}
	return true
}

func (rn *Runner) keepWebhook(message WebhookMessage) WebhookMessage {
	found := false
	for i := range rn.Store.Inbox {
		if rn.Store.Inbox[i].ID == message.ID {
			rn.Store.Inbox[i] = message
			found = true
			break
		}
	}
	if !found {
		rn.Store.Inbox = append(rn.Store.Inbox, message)
	}
	if len(rn.Store.Inbox) > WakeHistory {
		rn.Store.Inbox = rn.Store.Inbox[len(rn.Store.Inbox)-WakeHistory:]
	}
	rn.Persist()
	rn.host.Changed()
	if len(message.Routes) == 0 && message.Detail == "No matching automation" {
		rn.host.Deliver(notification.Notice{ID: "miya-webhook-" + message.ID, Title: tr("Webhook received"), Body: textutil.ClipRunes(message.Data, 180)})
	}
	return message
}

func WebhookDeliveryText(message WebhookMessage) string {
	if len(message.Routes) == 0 {
		if message.Detail == "No matching automation" {
			return tr("No matching automation")
		}
		return message.Detail
	}
	var results []string
	for _, r := range message.Routes {
		result := OutcomeLabel(r.Outcome)
		if r.Outcome == WakeGathered {
			result = tr("Gathering events")
		}
		if r.Detail != "" {
			result += " · " + r.Detail
		}
		results = append(results, r.Name+" · "+result)
	}
	return strings.Join(results, "\n")
}
