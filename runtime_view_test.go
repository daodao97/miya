package main

import (
	context "context"
	json "encoding/json"
	errors "errors"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	mobile "miya/internal/mobile"
	rt "miya/internal/runtime"
	desktopui "miya/internal/ui"
	webservice "miya/internal/webservice"
	widgets "miya/internal/widgets"
	net "net"
	httptest "net/http/httptest"
	strconv "strconv"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func sampleConversationRuntime(ref appmodel.ThreadRef) appmodel.ConversationRuntime {
	p := rt.Process{Identity: rt.Identity{Host: "desktop", Boot: "boot", Start: "start", PID: 42}, Name: "node", Status: "running"}
	return appmodel.ConversationRuntime{View: rt.View{Key: appmodel.RuntimeKey(ref), TasksKnown: true, Tasks: []rt.Task{{ID: "opaque", Kind: "local_bash", Description: "Start app", Status: "running"}}, Processes: []rt.Process{p}, ObservedAt: time.Now().UnixMilli()}, Services: []webservice.Service{{Port: 5173, Host: "127.0.0.1", Process: "node", RuntimeToken: "test-token"}}}
}

func TestMobileRuntimeIsConversationScopedAndHasBackgroundDetails(t *testing.T) {
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.State.Runtime = true
		m.State.WebPorts = true
		m.State.WebServices = true
		m.OpenThread(m.State.Projects[1].Threads[0])
		m.Draft = "保留草稿"
		tt := ui.NewTester(m.ViewRoot, width, 844)
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
		if !m.WebDetails || m.WebRef.SessionID != "s2" || m.WebProject != "/b" {
			t.Fatal("background details used another conversation")
		}
		sample := sampleConversationRuntime(m.WebRef)
		m.WebServices.Runtime = &sample
		m.WebServices.Services = sample.Services
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
		if m.Open != 2 || m.Draft != "保留草稿" {
			t.Fatal("runtime details changed conversation state")
		}
	}
}

func TestDesktopRuntimeSurfaceUsesSameView(t *testing.T) {
	app, tt := testApp(t)
	app.C.(*controller.Controller).SetThreads([]appmodel.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "claude-runtime", Title: "runtime", HistoryLoaded: true}})
	app.C.(*controller.Controller).SetActive(1)
	p := app.Panel()
	ref := app.C.(*controller.Controller).ThreadRef(app.C.(*controller.Controller).Current())
	sample := sampleConversationRuntime(ref)
	s := &desktopui.PanelSurface{Id: 1, Kind: desktopui.SurfaceRuntime, RuntimeRef: ref, RuntimeData: &sample, RuntimeAt: time.Now()}
	p.Visible, p.Active, p.Surfaces = true, 0, []*desktopui.PanelSurface{s}
	tt.Frame()
	for _, label := range []string{"Background activity", "Start app", "PID 42", "Open port 5173"} {
		if !tt.HasText(label) {
			t.Fatalf("desktop missing shared runtime view %s", label)
		}
	}
	widgets.RuntimeObservationLost(s.RuntimeData)
	tt.Frame()
	if tt.HasText("Open port 5173") || !tt.HasText("last verified result") || !tt.HasText("Awaiting verification") {
		t.Fatal("lost observation still displayed live endpoints")
	}
}

func TestPhoneRuntimeEndpointValidatesStableReferenceAndNeverFallsBack(t *testing.T) {
	root, fixture := fileServerFixture(t)
	handler := fixture.(appmodel.DesktopFileServer)
	ref := testRef(appmodel.AgentClaude, "claude-session")
	sample := sampleConversationRuntime(ref)
	calls := 0
	handler.Runtime = func(ctx context.Context, got appmodel.ThreadRef, project string) (appmodel.ConversationRuntime, error) {
		calls++
		if got != ref || project != root {
			return appmodel.ConversationRuntime{}, errors.New("unknown conversation")
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
	result, err := mobile.FetchPhoneRuntime(context.Background(), dial, root, ref)
	if err != nil || result.View.Key != appmodel.RuntimeKey(ref) || len(result.Services) != 1 {
		t.Fatalf("runtime endpoint: %+v %v", result, err)
	}
	if _, err := mobile.FetchPhoneRuntime(context.Background(), dial, root, testRef(appmodel.AgentClaude, "missing")); err == nil {
		t.Fatal("missing session accepted")
	}
	if _, err := mobile.FetchPhoneRuntime(context.Background(), dial, t.TempDir(), ref); err == nil {
		t.Fatal("unshared project accepted")
	}
	if calls != 2 {
		t.Fatalf("unexpected runtime calls %d", calls)
	}
}

func TestMCPRuntimeToolIsReadOnlyAndReturnsNoPreviewToken(t *testing.T) {
	var definition *appmodel.McpTool
	for i := range appmodel.McpTools() {
		if appmodel.McpTools()[i].Name == "runtime_info" {
			definition = &appmodel.McpTools()[i]
		}
	}
	if definition == nil || definition.Annotations["readOnlyHint"] != true {
		t.Fatal("runtime query is not advertised read-only")
	}
	server := appmodel.NewMCPServer("/untrusted", func(op string, args, out any) error {
		if op != "mcpRuntime" || args.(appmodel.McpRequest).Reference != "" {
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
