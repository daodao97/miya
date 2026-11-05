package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	rt "miya/internal/runtime"
	"miya/internal/webservice"
)

func sampleConversationRuntime(ref ThreadRef) conversationRuntime {
	p := rt.Process{Identity: rt.Identity{Host: "desktop", Boot: "boot", Start: "start", PID: 42}, Name: "node", Status: "running"}
	return conversationRuntime{View: rt.View{Key: runtimeKey(ref), TasksKnown: true, Tasks: []rt.Task{{ID: "opaque", Kind: "local_bash", Description: "Start app", Status: "running"}}, Processes: []rt.Process{p}, ObservedAt: time.Now().UnixMilli()}, Services: []webservice.Service{{Port: 5173, Host: "127.0.0.1", Process: "node", RuntimeToken: "test-token"}}}
}
func TestMobileRuntimeIsConversationScopedAndHasBackgroundDetails(t *testing.T) {
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.state.Runtime = true
		m.state.WebPorts = true
		m.state.WebServices = true
		m.openThread(m.state.Projects[1].Threads[0])
		m.draft = "保留草稿"
		tt := ui.NewTester(m.viewRoot, width, 844)
		tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
		tt.Frame()
		if err := tt.Click("More"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(time.Second)
		if err := tt.Click("Background tasks"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(time.Second)
		if !m.webDetails || m.webRef.SessionID != "s2" || m.webProject != "/b" {
			t.Fatal("background details used another conversation")
		}
		sample := sampleConversationRuntime(m.webRef)
		m.webServices.runtime = &sample
		m.webServices.services = sample.Services
		tt.Frame()
		for _, label := range []string{"Background tasks", "Start app", "System process", "PID 42", "Port 5173"} {
			if !tt.HasText(label) {
				t.Fatalf("missing runtime detail %q in %v", label, tt.Texts())
			}
		}
		saveMobileNativeSnapshot(t, tt, "runtime-mobile-"+strconv.Itoa(width))
		if err := tt.Click("Back"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(time.Second)
		if m.open != 2 || m.draft != "保留草稿" {
			t.Fatal("runtime details changed conversation state")
		}
	}
}
func TestDesktopRuntimeSurfaceUsesSameView(t *testing.T) {
	app, tt := testApp(t)
	app.c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "claude-runtime", Title: "runtime", HistoryLoaded: true}})
	app.c.setActive(1)
	p := app.panel()
	ref := app.c.threadRef(app.c.current())
	sample := sampleConversationRuntime(ref)
	s := &panelSurface{id: 1, kind: surfaceRuntime, runtimeRef: ref, runtimeData: &sample, runtimeAt: time.Now()}
	p.visible, p.active, p.surfaces = true, 0, []*panelSurface{s}
	tt.Frame()
	for _, label := range []string{"Background activity", "Start app", "PID 42", "Open port 5173"} {
		if !tt.HasText(label) {
			t.Fatalf("desktop missing shared runtime view %s", label)
		}
	}
	runtimeObservationLost(s.runtimeData)
	tt.Frame()
	if tt.HasText("Open port 5173") || !tt.HasText("last verified result") || !tt.HasText("Awaiting verification") {
		t.Fatal("lost observation still displayed live endpoints")
	}
}
func TestPhoneRuntimeEndpointValidatesStableReferenceAndNeverFallsBack(t *testing.T) {
	root, fixture := fileServerFixture(t)
	handler := fixture.(desktopFileServer)
	ref := testRef(agentClaude, "claude-session")
	sample := sampleConversationRuntime(ref)
	calls := 0
	handler.runtime = func(ctx context.Context, got ThreadRef, project string) (conversationRuntime, error) {
		calls++
		if got != ref || project != root {
			return conversationRuntime{}, errors.New("unknown conversation")
		}
		return sample, nil
	}
	handler.webServices = func(context.Context, string) ([]webservice.Service, error) {
		t.Error("runtime fell back to directory candidates")
		return nil, nil
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	result, err := fetchPhoneRuntime(context.Background(), dial, root, ref)
	if err != nil || result.View.Key != runtimeKey(ref) || len(result.Services) != 1 {
		t.Fatalf("runtime endpoint: %+v %v", result, err)
	}
	if _, err := fetchPhoneRuntime(context.Background(), dial, root, testRef(agentClaude, "missing")); err == nil {
		t.Fatal("missing session accepted")
	}
	if _, err := fetchPhoneRuntime(context.Background(), dial, t.TempDir(), ref); err == nil {
		t.Fatal("unshared project accepted")
	}
	if calls != 2 {
		t.Fatalf("unexpected runtime calls %d", calls)
	}
}
func TestMCPRuntimeToolIsReadOnlyAndReturnsNoPreviewToken(t *testing.T) {
	var definition *mcpTool
	for i := range mcpTools {
		if mcpTools[i].Name == "runtime_info" {
			definition = &mcpTools[i]
		}
	}
	if definition == nil || definition.Annotations["readOnlyHint"] != true {
		t.Fatal("runtime query is not advertised read-only")
	}
	server := newMCPServer("/untrusted", func(op string, args, out any) error {
		if op != "mcpRuntime" || args.(mcpRequest).Reference != "" {
			t.Fatal("runtime tool can redirect session")
		}
		raw, _ := json.Marshal(sampleConversationRuntime(testRef(agentCodex, "caller")).View)
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
