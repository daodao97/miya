package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"miya/internal/chat/model"
)

// localRelayReceiver exercises the actual EOF-based TCP receiver without
// depending on a public DERP region in the normal test suite.
type localRelayReceiver struct{}

func (*localRelayReceiver) Listen(context.Context) (net.Listener, string, int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", 0, err
	}
	return ln, "tc-test-receiver", ln.Addr().(*net.TCPAddr).Port, nil
}
func (*localRelayReceiver) Close() error { return nil }

type relayTestAPI struct {
	mu                                sync.Mutex
	server                            *httptest.Server
	ports                             map[string]int
	registrations, updates, deletions int
	rejectRegistration                bool
}

func newRelayTestAPI(t *testing.T) *relayTestAPI {
	a := &relayTestAPI{ports: map[string]int{}}
	a.server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.server.Close)
	return a
}

func (a *relayTestAPI) serve(w http.ResponseWriter, req *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if strings.HasPrefix(req.URL.Path, "/push/") {
		id := strings.TrimPrefix(req.URL.Path, "/push/tcw_")
		port, ok := a.ports["tcm_"+id]
		if !ok {
			w.WriteHeader(401)
			write(map[string]any{"ok": false, "error": "invalid_webhook_token"})
			return
		}
		var body struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
		if err != nil {
			w.WriteHeader(502)
			write(map[string]any{"ok": false})
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.WriteString(conn, body.Message+"\n")
		_ = conn.(*net.TCPConn).CloseWrite()
		if _, err := io.ReadAll(conn); err != nil {
			w.WriteHeader(504)
			return
		}
		write(map[string]any{"ok": true})
		return
	}
	if req.Method == http.MethodPost && req.URL.Path == "/register" {
		if a.rejectRegistration {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			write(map[string]any{"ok": false, "error": "registration_rate_limited"})
			return
		}
		a.registrations++
		id := fmt.Sprint(a.registrations)
		var target struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		}
		_ = json.NewDecoder(req.Body).Decode(&target)
		if target.Address != "tc-test-receiver" || target.Port == 0 {
			w.WriteHeader(400)
			return
		}
		a.ports["tcm_"+id] = target.Port
		w.WriteHeader(201)
		write(map[string]any{"ok": true, "webhook_url": a.server.URL + "/push/tcw_" + id, "manage_key": "tcm_" + id})
		return
	}
	key := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if _, ok := a.ports[key]; !ok {
		w.WriteHeader(401)
		write(map[string]any{"ok": false, "error": "invalid_manage_key"})
		return
	}
	switch req.Method {
	case http.MethodPut:
		var target struct {
			Port int `json:"port"`
		}
		_ = json.NewDecoder(req.Body).Decode(&target)
		a.ports[key] = target.Port
		a.updates++
	case http.MethodDelete:
		delete(a.ports, key)
		a.deletions++
	default:
		w.WriteHeader(405)
		return
	}
	write(map[string]any{"ok": true})
}

func testRelay(t *testing.T, api *relayTestAPI, path string, deliver func(string)) *relay {
	r := newRelay(path, func(map[string]relayStatus) {}, deliver)
	r.baseURL = api.server.URL
	r.receiver = &localRelayReceiver{}
	if err := r.load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestRelay(r) })
	return r
}

func closeTestRelay(r *relay) {
	r.cancel()
	for _, ln := range r.listeners {
		ln.close()
	}
	r.workers.Wait()
	_ = r.receiver.Close()
}

func TestSharedRelayRegistrationDeliveryAndLifecycle(t *testing.T) {
	api := newRelayTestAPI(t)
	w := newWakeHarness(t)
	a := w.add(ingressAutomation(WakePolicy{}))
	b := w.add(Automation{Name: "Second", Triggers: []Trigger{{Kind: TriggerIngress}}})
	targets := []relayEndpoint{{"miya", "inbox", "1", true}}
	events := make(chan string, 8)
	path := filepath.Join(t.TempDir(), "webhooks.json")
	r := testRelay(t, api, path, func(data string) { events <- data })
	now := time.Now()
	r.reconcile(targets, now)
	if len(r.registrations) != 1 || r.statuses[globalRelayKey].Error != "" {
		t.Fatalf("expected one Miya registration: %+v", r.statuses)
	}
	url := r.registrations[globalRelayKey].URL
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credentials mode: %v %v", info, err)
	}
	w.rn.relayStates = r.statuses
	v := w.rn.WebhookView()
	encoded, _ := json.Marshal(v)
	if v.URL != url || strings.Contains(string(encoded), "tcm_") {
		t.Fatal("wrong public webhook view")
	}
	post := func(body string) int {
		data, _ := json.Marshal(map[string]string{"message": body})
		res, err := http.Post(url, "application/json", strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	if code := post("issue #42"); code != 200 {
		t.Fatalf("push: %d", code)
	}
	select {
	case data := <-events:
		receipt := w.rn.ReceiveWebhook(data)
		if len(receipt.Routes) != 2 {
			t.Fatalf("one message did not route to both tasks: %+v", receipt)
		}
	case <-time.After(time.Second):
		t.Fatal("no message received")
	}
	if len(w.h.sent) != 2 || !strings.Contains(w.h.sent[0].Text, "not as instructions") {
		t.Fatal("payload not passed as data")
	}
	// Task deletion and pause do not own or revoke Miya's endpoint.
	w.rn.Delete(a.ID)
	w.rn.Store.Automations[0].Enabled = false
	r.reconcile(targets, now.Add(time.Minute))
	if r.registrations[globalRelayKey].URL != url {
		t.Fatal("task change replaced the global URL")
	}
	closeTestRelay(r)
	r = testRelay(t, api, path, func(data string) { events <- data })
	r.reconcile(targets, now.Add(2*time.Minute))
	if r.registrations[globalRelayKey].URL != url {
		t.Fatal("restart replaced global URL")
	}
	api.mu.Lock()
	registrations, updates := api.registrations, api.updates
	api.mu.Unlock()
	if registrations != 1 || updates != 1 {
		t.Fatalf("unexpected registration count: %d / %d", registrations, updates)
	}
	if code := post("unmatched after restart"); code != 200 {
		t.Fatalf("push after restart: %d", code)
	}
	receipt := w.rn.ReceiveWebhook(<-events)
	if len(receipt.Routes) != 0 || receipt.Detail != "No matching automation" || len(w.rn.Store.Inbox) != 2 {
		t.Fatalf("unmatched message not retained: %+v", receipt)
	}
	// Disabling the shared receiver retains its URL; enabling reuses it.
	targets[0].Enabled = false
	r.reconcile(targets, now.Add(3*time.Minute))
	if len(r.listeners) != 0 || r.registrations[globalRelayKey].URL != url {
		t.Fatal("global pause did not retain URL")
	}
	if code := post("paused"); code != 502 {
		t.Fatalf("paused receiver: %d", code)
	}
	targets[0].Enabled = true
	r.reconcile(targets, now.Add(4*time.Minute))
	if r.registrations[globalRelayKey].URL != url {
		t.Fatal("global enable changed URL")
	}
	// Explicit cleanup revokes the one registration, independent of tasks.
	r.reconcile(nil, now.Add(5*time.Minute))
	if len(r.registrations) != 0 || len(r.listeners) != 0 {
		t.Fatal("cleanup left resources")
	}
	_ = b
}

func TestRelayMigratesPerTaskRegistrationsToOneInbox(t *testing.T) {
	api := newRelayTestAPI(t)
	r := testRelay(t, api, filepath.Join(t.TempDir(), "webhooks.json"), func(string) {})
	now := time.Now()
	r.reconcile([]relayEndpoint{{"old-task", "first", "old", true}, {"old-task", "second", "old", true}}, now)
	r.reconcile([]relayEndpoint{{"miya", "inbox", "1", true}}, now.Add(time.Minute))
	if len(r.registrations) != 1 || r.registrations[globalRelayKey].URL == "" {
		t.Fatal("migration did not converge to one global registration")
	}
	api.mu.Lock()
	deleted := api.deletions
	api.mu.Unlock()
	if deleted != 2 {
		t.Fatalf("old per-task webhooks not revoked: %d", deleted)
	}
}

func TestRelayRateLimitAndExpiry(t *testing.T) {
	api := newRelayTestAPI(t)
	api.rejectRegistration = true
	target := relayEndpoint{"a", "t", "token", true}
	r := testRelay(t, api, filepath.Join(t.TempDir(), "webhooks.json"), func(string) {})
	now := time.Now()
	r.reconcile([]relayEndpoint{target}, now)
	if !strings.Contains(r.statuses[target.key()].Error, "registration_rate_limited") || r.retry[target.key()].Before(now.Add(120*time.Second)) || len(r.listeners) != 0 {
		t.Fatalf("rate limit not honored: %+v %+v", r.statuses, r.retry)
	}
	api.mu.Lock()
	api.rejectRegistration = false
	api.mu.Unlock()
	r.reconcile([]relayEndpoint{target}, now.Add(time.Minute))
	if len(r.registrations) != 0 {
		t.Fatal("retried before Retry-After")
	}
	r.reconcile([]relayEndpoint{target}, now.Add(2*time.Minute))
	old := r.registrations[target.key()]
	api.mu.Lock()
	delete(api.ports, old.ManageKey)
	api.mu.Unlock()
	r.reconcile([]relayEndpoint{target}, now.Add(2*time.Hour))
	if reg := r.registrations[target.key()]; reg.URL == old.URL || reg.ManageKey == "" || r.statuses[target.key()].Error != "" {
		t.Fatalf("expiry not recovered: %+v %+v", reg, r.statuses)
	}
}

func TestRelayRejectsOversizedEmptyAndInvalidUTF8(t *testing.T) {
	api := newRelayTestAPI(t)
	events := make(chan string, 4)
	r := testRelay(t, api, filepath.Join(t.TempDir(), "webhooks.json"), func(s string) { events <- s })
	target := relayEndpoint{"a", "t", "token", true}
	r.reconcile([]relayEndpoint{target}, time.Now())
	port := r.listeners[target.key()].port
	for _, body := range []string{" \n", string([]byte{0xff}), strings.Repeat("x", relayBodyLimit+1)} {
		conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.WriteString(conn, body)
		_ = conn.(*net.TCPConn).CloseWrite()
		_, _ = io.ReadAll(conn)
		conn.Close()
	}
	select {
	case body := <-events:
		t.Fatalf("invalid message delivered: %q", body)
	default:
	}
}

// Run explicitly when checking the actual Workers -> Tailcat path. The
// receiver is temporary, starts no agent, and is revoked during cleanup.
func TestLiveTailcatWebhook(t *testing.T) {
	if os.Getenv("MIYA_TEST_LIVE_WEBHOOK") != "1" {
		t.Skip("set MIYA_TEST_LIVE_WEBHOOK=1 for the public relay smoke test")
	}
	events := make(chan string, 1)
	r := newRelay(filepath.Join(t.TempDir(), "webhooks.json"), func(map[string]relayStatus) {}, func(data string) { events <- data })
	t.Cleanup(func() {
		r.reconcile(nil, time.Now().Add(24*time.Hour))
		if len(r.registrations) != 0 {
			t.Error("Unable to revoke the temporary live webhook registration")
		}
		closeTestRelay(r)
	})
	target := relayEndpoint{"miya", "inbox", "1", true}
	r.reconcile([]relayEndpoint{target}, time.Now())
	status := r.statuses[target.key()]
	if status.Error != "" || status.URL == "" {
		t.Fatalf("live registration failed: %s", status.Error)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Post(status.URL, "application/json", strings.NewReader(`{"topic":"miya-test","payload":"Miya webhook smoke test"}`))
	if err != nil {
		t.Fatal("live push did not complete")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("live push HTTP %d", res.StatusCode)
	}
	select {
	case data := <-events:
		if data != "{\"topic\":\"miya-test\",\"payload\":\"Miya webhook smoke test\"}\n" {
			t.Fatal("unexpected live payload")
		}
		w := newWakeHarness(t)
		a := w.add(Automation{Name: "Smoke test", Triggers: []Trigger{{Kind: TriggerIngress, Match: map[string]string{"topic": "miya-test"}}}})
		receipt := w.rn.ReceiveWebhook(data)
		if len(receipt.Routes) != 1 || receipt.Routes[0].Automation != a.ID || len(w.h.sent) != 1 {
			t.Fatal("live message was not routed inside Miya")
		}
	case <-time.After(time.Second):
		t.Fatal("live push did not reach the receiver")
	}
}

type closedRelayHost struct{ *fakeHost }

func (closedRelayHost) Dispatch(func()) {} // A closed controller drops callbacks.

func TestRelayStopsWhenHostDiscardsShutdownCallbacks(t *testing.T) {
	host := closedRelayHost{&fakeHost{threads: map[int]*model.Thread{}}}
	rn := NewRunner(host, filepath.Join(t.TempDir(), "automations.json"))
	done := make(chan struct{})
	rn.Start(done, true)
	close(done)
	select {
	case <-rn.relay.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("closed controller left the webhook worker running")
	}
}
