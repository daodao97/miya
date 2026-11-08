package mobile

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/interaction"
	phoneproto "miya/internal/phone/protocol"
)

func TestTakeoverPageUsesRequestActionsWithoutAFormSchema(t *testing.T) {
	for _, label := range []string{"Done, continue", "Cancel takeover"} {
		t.Run(label, func(t *testing.T) {
			m := NewMobileApp()
			m.ApplyPalette(false)
			a := interaction.Request{ID: json.RawMessage(`"manual"`), Category: interaction.DesktopControl, Reason: "Finish browser sign-in"}
			m.State = phoneproto.State{Approvals: []interaction.Request{a}}
			m.approvalID = a.ID
			tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { m.approvalPage(c) }) }, 390, 700)
			for _, name := range []string{"Done, continue", "Cancel takeover"} {
				r, ok := tt.Find(name)
				if !ok || r.Y+r.H > 700 || r.H < 44 {
					t.Fatalf("missing reachable takeover action: %s %v", name, r)
				}
			}
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			if m.FormErrors[string(a.ID)] != "Not connected to desktop" {
				t.Fatalf("takeover tried to validate an unrelated form instead of submitting: %q", m.FormErrors[string(a.ID)])
			}
		})
	}
}
