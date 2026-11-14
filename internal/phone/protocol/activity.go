package protocol

import (
	"encoding/hex"
	"net/url"

	"miya/internal/chat/model"
)

// ActivityRegistration subscribes an NSGo widget to desktop-owned APNs updates.
// DeviceID namespaces IDs between installations; an empty Token unregisters.
// Reference is a durable miya thread URL, never a desktop-local thread number.
type ActivityRegistration struct {
	Desktop   string `json:"desktop,omitempty"`
	DeviceID  string `json:"deviceId"`
	ID        string `json:"id"`
	Token     string `json:"token,omitempty"`
	Reference string `json:"reference,omitempty"`
}

// ActivityURL includes the phone's desktop identity so tapping a widget can
// select a paired desktop before resolving its durable conversation reference.
func ActivityURL(reference, desktop string) string {
	if desktop == "" {
		return reference
	}
	q := url.Values{"reference": {reference}, "desktop": {desktop}}
	return "miya://notification?" + q.Encode()
}

func ParseActivityURL(raw string) (reference, desktop string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "miya" || u.Host != "notification" || u.User != nil || u.Path != "" || u.Fragment != "" {
		return "", "", false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 2 || len(q["reference"]) != 1 || len(q["desktop"]) != 1 {
		return "", "", false
	}
	reference, desktop = q.Get("reference"), q.Get("desktop")
	_, ok = model.ParseThreadURL(reference)
	if len(desktop) == 0 || len(desktop) > 64 {
		return "", "", false
	}
	if _, err = hex.DecodeString(desktop); err != nil {
		return "", "", false
	}
	return reference, desktop, ok
}
