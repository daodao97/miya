//go:build !mygo_ui

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	mcpserver "miya/internal/mcp"
	mcpproto "miya/internal/mcp/protocol"
	phoneserver "miya/internal/phone/server"
	phoneservices "miya/internal/phone/services"
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func sampleConversationRuntime(ref model.ThreadRef) tracker.ConversationRuntime {
	p := rt.Process{Identity: rt.Identity{Host: "desktop", Boot: "boot", Start: "start", PID: 42}, Name: "node", Status: "running"}
	return tracker.ConversationRuntime{View: rt.View{Key: tracker.KeyOf(ref), TasksKnown: true, Tasks: []rt.Task{{ID: "opaque", Kind: "local_bash", Description: "Start app", Status: "running"}}, Processes: []rt.Process{p}, ObservedAt: time.Now().UnixMilli()}, Services: []webservice.Service{{Port: 5173, Host: "127.0.0.1", Process: "node", RuntimeToken: "test-token"}}}
}

func TestPhoneRuntimeEndpointValidatesStableReferenceAndNeverFallsBack(t *testing.T) {
	root, fixture := fileServerFixture(t)
	handler := fixture.(phoneserver.DesktopFileServer)
	ref := testRef(appmodel.AgentClaude, "claude-session")
	sample := sampleConversationRuntime(ref)
	calls := 0
	handler.Runtime = func(ctx context.Context, got model.ThreadRef, project string) (tracker.ConversationRuntime, error) {
		calls++
		if got != ref || project != root {
			return tracker.ConversationRuntime{}, errors.New("unknown conversation")
		}
		return sample, nil
	}
	handler.WebServices = func(context.Context, string) ([]webservice.Service, error) {
		t.Error("runtime fell back to directory candidates")
		return nil, nil
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	result, err := phoneservices.FetchPhoneRuntime(context.Background(), dial, root, ref)
	if err != nil || result.View.Key != tracker.KeyOf(ref) || len(result.Services) != 1 {
		t.Fatalf("runtime endpoint: %+v %v", result, err)
	}
	if _, err := phoneservices.FetchPhoneRuntime(context.Background(), dial, root, testRef(appmodel.AgentClaude, "missing")); err == nil {
		t.Fatal("missing session accepted")
	}
	if _, err := phoneservices.FetchPhoneRuntime(context.Background(), dial, t.TempDir(), ref); err == nil {
		t.Fatal("unshared project accepted")
	}
	if calls != 2 {
		t.Fatalf("unexpected runtime calls %d", calls)
	}
}

func TestMCPRuntimeToolIsReadOnlyAndReturnsNoPreviewToken(t *testing.T) {
	var definition *mcpproto.Tool
	for i := range mcpserver.Tools() {
		if mcpserver.Tools()[i].Name == "runtime_info" {
			definition = &mcpserver.Tools()[i]
		}
	}
	if definition == nil || definition.Annotations["readOnlyHint"] != true {
		t.Fatal("runtime query is not advertised read-only")
	}
	server := mcpserver.NewServer("/untrusted", func(op string, args, out any) error {
		if op != "mcpRuntime" || args.(mcpproto.Request).Reference != "" {
			t.Fatal("runtime tool can redirect session")
		}
		raw, _ := json.Marshal(sampleConversationRuntime(testRef(appmodel.AgentCodex, "caller")).View)
		*out.(*json.RawMessage) = raw
		return nil
	}, nil)
	text, err := server.CallTool("runtime_info", json.RawMessage(`{"id":"miya://spoofed"}`))
	if err != nil || text == "" {
		t.Fatal(err)
	}
	var view rt.View
	if json.Unmarshal([]byte(text), &view) != nil || view.Key.SessionID != "caller" {
		t.Fatal("invalid runtime result")
	}
}
