package nsui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/chat/model"
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
	svcproto "miya/internal/service/protocol"
)

func runtimeHost() *fakeHost {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"}, model.Thread{ID: 2, Title: "Beta"})
	h.files = newPanelFiles()
	return h
}

func runtimeFixture() tracker.ConversationRuntime {
	return tracker.ConversationRuntime{View: rt.View{
		TasksKnown: true, ObservedAt: 1000,
		Tasks:     []rt.Task{{ID: "build", Description: "Building app", Status: "running", Kind: "shell"}},
		Processes: []rt.Process{{Name: "node", Status: "running", Identity: rt.Identity{PID: 42}}},
		Endpoints: []rt.Endpoint{{Host: "127.0.0.1", Port: 9000, Protocol: "tcp"}},
	}, Services: []webservice.Service{{Host: "127.0.0.1", Port: 8080, RuntimeToken: "verified-token"}}}
}

func TestRuntimeTabRefreshAndObservationLost(t *testing.T) {
	h := runtimeHost()
	var reads atomic.Int32
	fixture := runtimeFixture()
	h.readRuntime = func(ctx context.Context, ref model.ThreadRef, project string) (tracker.ConversationRuntime, error) {
		if project != "/workspace" || ref.SessionID != "1" {
			return tracker.ConversationRuntime{}, errors.New("wrong runtime identity")
		}
		if reads.Add(1) > 1 {
			return tracker.ConversationRuntime{}, errors.New("observer offline")
		}
		return fixture, nil
	}
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-runtime")
	app.WaitFor("id:runtime-open-8080", time.Second)
	for _, want := range []string{"Building app", "Running · Background command", "node · PID 42 · Running", "127.0.0.1:9000 · TCP", "Last verified "} {
		if !strings.Contains(joinedTexts(app), want) {
			t.Fatalf("missing %q in %q", want, app.Texts())
		}
	}
	app.Press("id:runtime-refresh")
	app.WaitForGone("id:runtime-open-8080", time.Second)
	for _, want := range []string{"observer offline", "Awaiting verification · Background command", "node · PID 42 · Awaiting verification", "Runtime information cannot be updated. Showing the last verified result."} {
		if !strings.Contains(joinedTexts(app), want) {
			t.Fatalf("missing stale observation %q in %q", want, app.Texts())
		}
	}
	if strings.Contains(joinedTexts(app), "9000 · TCP") {
		t.Fatal("stale endpoint still advertised")
	}
	if fixture.View.Tasks[0].Status != "running" {
		t.Fatal("refresh failure mutated the host's snapshot")
	}
	// Polls use the NSGo clock and stop when the tab goes away.
	app.Advance(5 * time.Second)
	app.WaitUntil(time.Second, func() bool { return reads.Load() == 3 })
	app.Press("id:panel-close-1")
	app.Advance(5 * time.Second)
	app.Settle()
	if reads.Load() != 3 {
		t.Fatal("closed runtime tab still polling")
	}
}

func TestRuntimeServiceValidatedBeforeOpeningBrowser(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "rejected"}[reject], func(t *testing.T) {
			h := runtimeHost()
			h.readRuntime = func(context.Context, model.ThreadRef, string) (tracker.ConversationRuntime, error) {
				return runtimeFixture(), nil
			}
			requests := make(chan svcproto.Request, 1)
			h.state.Remote = settingsTestService(t, func(req svcproto.Request) (any, error) {
				if req.Op == "hello" {
					return svcproto.Hello{}, nil
				}
				requests <- req
				if reject {
					return nil, errors.New("service identity changed")
				}
				return nil, nil
			})
			app := startBridged(t, h)
			app.Press("role:button Show / Hide right panel")
			app.Press("id:panel-open-runtime")
			app.WaitFor("id:runtime-open-8080", time.Second)
			app.Press("id:runtime-open-8080")
			var req svcproto.Request
			select {
			case req = <-requests:
			case <-time.After(time.Second):
				t.Fatal("validation never requested")
			}
			var service webservice.Service
			if json.Unmarshal(req.Args, &service) != nil || req.Op != "runtimeValidate" || service.RuntimeToken != "verified-token" || service.Port != 8080 {
				t.Fatalf("wrong validation: %+v", req)
			}
			if reject {
				app.WaitUntil(time.Second, func() bool { return strings.Contains(joinedTexts(app), "service identity changed") })
				if app.Exists("id:panel-tab-2") {
					t.Fatal("browser opened without validation")
				}
			} else {
				app.WaitFor("id:browser-2", time.Second)
				if got := app.Find("id:browser-address-2").Text; got != "http://127.0.0.1:8080/" {
					t.Fatalf("browser address = %q", got)
				}
			}
		})
	}
}

func TestRuntimeDraftAndLateReadStayWithConversation(t *testing.T) {
	h := runtimeHost()
	started, release := make(chan struct{}), make(chan struct{})
	h.readRuntime = func(ctx context.Context, ref model.ThreadRef, project string) (tracker.ConversationRuntime, error) {
		if ref.SessionID == "1" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return tracker.ConversationRuntime{}, ctx.Err()
			}
		}
		result := runtimeFixture()
		result.View.Tasks[0].Description = "Runtime for " + ref.SessionID
		return result, nil
	}
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-runtime")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runtime read not started")
	}
	app.Press("id:thread-2")
	app.WaitFor("id:panel-chooser", time.Second)
	app.Press("id:panel-open-runtime")
	app.WaitUntil(time.Second, func() bool { return strings.Contains(joinedTexts(app), "Runtime for 2") })
	close(release)
	app.Settle()
	if strings.Contains(joinedTexts(app), "Runtime for 1") {
		t.Fatal("late runtime result leaked into another conversation")
	}
}

func TestRuntimeDraftExplainsMissingIdentity(t *testing.T) {
	h := newFakeHost(model.Thread{})
	var reads atomic.Int32
	h.readRuntime = func(context.Context, model.ThreadRef, string) (tracker.ConversationRuntime, error) {
		reads.Add(1)
		return tracker.ConversationRuntime{}, nil
	}
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-runtime")
	app.Settle()
	if !strings.Contains(joinedTexts(app), "Send a message to start a conversation and view runtime information") || reads.Load() != 0 {
		t.Fatal("draft tried to read runtime without an identity")
	}
}

func TestMirroredRuntimeServiceRetainsBindingWhenAddressPathChanges(t *testing.T) {
	h := runtimeHost()
	h.remoteHost = true
	h.readRuntime = func(context.Context, model.ThreadRef, string) (tracker.ConversationRuntime, error) {
		result := runtimeFixture()
		result.Services[0].Host = "192.168.0.4"
		return result, nil
	}
	h.state.Remote = settingsTestService(t, func(req svcproto.Request) (any, error) { return nil, nil })
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-runtime")
	app.WaitFor("id:runtime-open-8080", time.Second)
	app.Press("id:runtime-open-8080")
	app.WaitFor("id:browser-address-2", time.Second)
	// This fake service can validate but has no file tunnel. Both the first
	// load and path edits must report that; neither may load the client's LAN.
	for _, address := range []string{"http://192.168.0.4:8080/", "http://192.168.0.4:8080/settings?q=1"} {
		app.Clear("id:browser-address-2")
		app.Type("id:browser-address-2", address)
		app.Submit("id:browser-address-2")
		app.Settle()
		if app.Exists("id:browser-2") || !strings.Contains(joinedTexts(app), "Browser unavailable") {
			t.Fatalf("%s lost its remote runtime binding", address)
		}
	}
	// An unrelated public origin must still load directly.
	app.Clear("id:browser-address-2")
	app.Type("id:browser-address-2", "https://example.com/")
	app.Submit("id:browser-address-2")
	app.WaitFor("id:browser-2", time.Second)
}
