package access_test

import (
	"encoding/json"
	"errors"
	"testing"

	"miya/internal/interaction"
	"miya/internal/mcp/access"
)

type forwardedHost struct {
	id  json.RawMessage
	err error
}

func (h *forwardedHost) Forward(id json.RawMessage) (bool, error) { h.id = id; return true, h.err }
func (*forwardedHost) Pending() []interaction.Request {
	panic("forwarded requests must use the remote queue")
}
func (*forwardedHost) Respond(interaction.Request, any) {
	panic("forwarded requests must not reply locally")
}

func TestForwardedGrantDoesNotCreateLocalPermission(t *testing.T) {
	var grants access.Grants
	request := interaction.Request{ID: json.RawMessage(`1`), Method: "mcpServer/elicitation/request", ThreadID: "thread", ServerName: "cua_repl", Message: `Allow Computer Use to use "Xcode"?`, Mode: "form", RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`)}
	if !access.Rememberable(request) {
		t.Fatal("invalid access-request fixture")
	}
	host := &forwardedHost{}
	if err := grants.Confirm(request, host); err != nil {
		t.Fatal(err)
	}
	if string(host.id) != "1" || grants.Remembered(request) {
		t.Fatal("a remote grant was stored locally")
	}
	host.err = errors.New("remote unavailable")
	if err := grants.Confirm(request, host); !errors.Is(err, host.err) || grants.Remembered(request) {
		t.Fatalf("remote failure was hidden or granted access: %v", err)
	}
}
