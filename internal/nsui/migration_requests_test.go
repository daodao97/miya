package nsui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/interaction"
	"miya/internal/nswidgets"
)

type consentHost struct {
	answerHost
	remembered int
}

func (h *consentHost) RespondMCPAlways(interaction.Request) error { h.remembered++; return nil }
func TestMigratedMCPAccessRequiresExplicitRememberedConsent(t *testing.T) {
	for _, remember := range []bool{false, true} {
		t.Run(map[bool]string{false: "once", true: "session"}[remember], func(t *testing.T) {
			h := new(consentHost)
			a := interaction.Request{ID: json.RawMessage(`1`), Method: "mcpServer/elicitation/request", ThreadID: "thread-a", TurnID: "turn-1", ServerName: "cua_repl", Mode: "form", Message: `Allow Computer Use to use "Xcode"?`, RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`)}
			app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
				cx.SetTheme(nswidgets.Theme())
				return pendingRequests(cx, h, func() []interaction.Request { return []interaction.Request{a} })
			}, nsgotest.Size(700, 800))
			if h.remembered != 0 || len(h.answered) != 0 || strings.Contains(strings.Join(app.Texts(), " "), "MCP form JSON") {
				t.Fatal("render granted access or exposed empty schema")
			}
			if remember {
				app.Press("label:Always allow in this conversation")
				if h.remembered != 1 || len(h.answered) != 0 {
					t.Fatal("remembered consent not routed")
				}
			} else {
				app.Press("id:confirm")
				if h.remembered != 0 || len(h.answered) != 1 || h.answered[0].Action != interaction.Submit {
					t.Fatal("allow once created grant")
				}
			}
		})
	}
}
