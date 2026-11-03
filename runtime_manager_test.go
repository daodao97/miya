//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"miya/internal/claude"
	"miya/internal/codex"
	rt "miya/internal/runtime"
)

func TestCodexRuntimeRecoversTaggedServicesWithoutNativeTerminals(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_THREAD_ID", "detached-a")
	pidA, portA, _ := startRuntimeHTTP(t, dir)
	t.Setenv("CODEX_THREAD_ID", "detached-b")
	_, portB, _ := startRuntimeHTTP(t, dir)
	t.Setenv("CODEX_THREAD_ID", "")
	_, unowned, _ := startRuntimeHTTP(t, dir)
	if got := rt.CodexProcessSession(pidA); got != "detached-a" {
		t.Fatalf("process marker = %q", got)
	}
	m := newRuntimeManager(nil, "")
	defer m.Close()
	for _, tc := range []struct {
		session string
		port    int
	}{{"detached-a", portA}, {"detached-b", portB}} {
		key := runtimeKey(testRef(agentCodex, tc.session))
		m.register(key, runtimeSource{Epoch: "e", Caps: rt.Capabilities{SessionTag: true}, Snapshot: func(context.Context) (runtimeSnapshot, error) {
			return runtimeSnapshot{Known: true, Tasks: []rt.Task{}}, nil
		}})
		result := runtimeResult(m, key, m.refresh(context.Background(), key))
		if len(result.Services) != 1 || result.Services[0].Port != tc.port || !result.View.TasksKnown || len(result.View.Tasks) != 0 {
			t.Fatalf("tagged recovery failed (unowned port %d): %+v", unowned, result)
		}
		if result.View.Processes[0].Evidence != "session-tag" {
			t.Fatal("missing marker evidence")
		}
	}
}

func TestRuntimeOwnedServiceOnDesktopLANInterface(t *testing.T) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	host := ""
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && !ip.IsLoopback() {
			host = ip.String()
			break
		}
	}
	if host == "" {
		t.Skip("no LAN IPv4 interface")
	}
	t.Setenv("MIYA_WEB_DISCOVERY_HOST", host)
	t.Setenv("CODEX_THREAD_ID", "lan-session")
	_, port, _ := startRuntimeHTTP(t, t.TempDir())
	m := newRuntimeManager(nil, "")
	defer m.Close()
	key := runtimeKey(testRef(agentCodex, "lan-session"))
	m.register(key, runtimeSource{Epoch: "e", Caps: rt.Capabilities{SessionTag: true}, Snapshot: func(context.Context) (runtimeSnapshot, error) { return runtimeSnapshot{Known: true}, nil }})
	result := runtimeResult(m, key, m.refresh(context.Background(), key))
	if len(result.Services) != 1 || result.Services[0].Host != host || result.Services[0].Port != port {
		t.Fatalf("LAN listener missing: %+v", result)
	}
	svc := result.Services[0]
	request := httptest.NewRequest(http.MethodGet, desktopWebPrefix+strconv.Itoa(port)+"/", nil)
	request.Header.Set("X-Miya-Web-Host", host)
	request.Header.Set("X-Miya-Runtime", svc.RuntimeToken)
	response := httptest.NewRecorder()
	desktopWebServer(m.validateToken).ServeHTTP(response, request)
	if response.Code != 404 {
		t.Fatalf("owned LAN proxy = %d: %s", response.Code, response.Body.String())
	}
	request.Header.Del("X-Miya-Runtime")
	response = httptest.NewRecorder()
	desktopWebServer(m.validateToken).ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatal("LAN proxy accepted missing ownership token")
	}
	request.Header.Set("X-Miya-Runtime", svc.RuntimeToken)
	request.Header.Set("X-Miya-Web-Host", "203.0.113.1")
	response = httptest.NewRecorder()
	desktopWebServer(m.validateToken).ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatal("LAN proxy accepted another computer")
	}
}

func startRuntimeHTTP(t *testing.T, dir string) (pid, port int, stop func()) {
	t.Helper()
	exe, _ := os.Executable()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestProjectWebDiscoveryHelper$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MIYA_WEB_DISCOVERY_HELPER=http")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); _ = cmd.Wait() }) }
	t.Cleanup(stop)
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	port, err = strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid, port, stop
}
func TestCodexRuntimeUsesNativeSessionPIDsOnSharedProject(t *testing.T) {
	dir := t.TempDir()
	pidA, portA, _ := startRuntimeHTTP(t, dir)
	pidB, portB, _ := startRuntimeHTTP(t, dir)
	metadata := map[string]any{"resume-thread": []map[string]any{{"processId": "1234", "itemId": "tool-a", "osPid": pidA, "command": "web app"}}, "session-b": []map[string]any{{"processId": "1234", "itemId": "tool-b", "osPid": pidB, "command": "api"}}}
	data, _ := json.Marshal(metadata)
	t.Setenv("QUICKGUI_FIXTURE_BACKGROUND", string(data))
	c, _, _ := codexResumeFixture(t)
	c.setThreads([]Thread{{ID: 1, ServerID: "resume-thread"}, {ID: 2, ServerID: "session-b"}})
	for _, tc := range []struct {
		id          int
		port, other int
	}{{1, portA, portB}, {2, portB, portA}} {
		work, err := c.prepareRuntime(c.threadRef(c.threadWithID(tc.id)), c.cwd)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := work()
		if err != nil {
			t.Fatal(err)
		}
		result := raw.(conversationRuntime)
		if !result.View.TasksKnown || len(result.Services) != 1 || result.Services[0].Port != tc.port {
			t.Fatalf("wrong Codex session resources: %+v", result)
		}
		for _, endpoint := range result.View.Endpoints {
			if endpoint.Port == tc.other {
				t.Fatal("shared app-server leaked another conversation's service")
			}
		}
	}
	if _, err := c.prepareRuntime(testRef(agentCodex, "resume-thread"), "/other-project"); err == nil {
		t.Fatal("runtime accepted a mismatched project")
	}
	foreign := c.threadRef(c.threadWithID(1))
	foreign.Source = "ffffffffffffffffffffffffffffffff"
	if _, err := c.prepareRuntime(foreign, c.cwd); err == nil {
		t.Fatal("runtime accepted another desktop")
	}
}

const runtimeClaudeFake = `#!/bin/sh
read -r line
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'
read -r line
MIYA_WEB_DISCOVERY_HELPER=http "$MIYA_RUNTIME_TEST_BINARY" -test.run=^TestProjectWebDiscoveryHelper$ > "$MIYA_RUNTIME_TEST_PORT" &
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"bash","name":"Bash","input":{"command":"web app","run_in_background":true}}]}}'
printf '%s\n' '{"type":"system","subtype":"task_started","task_id":"1234","tool_use_id":"bash","task_type":"local_bash"}'
printf '%s\n' '{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"1234","task_type":"local_bash","description":"Web app"}]}'
printf '%s\n' '{"type":"result","result":"Started"}'
while read -r line; do :; done
`

func startRuntimeClaude(t *testing.T, ctx context.Context, dir string) (*claude.Client, int) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(runtimeClaudeFake), 0700); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	portFile := filepath.Join(t.TempDir(), "port")
	result := make(chan struct{}, 1)
	client, err := claude.Start(ctx, claude.Options{Binary: bin, Cwd: dir, Env: []string{"MIYA_RUNTIME_TEST_BINARY=" + exe, "MIYA_RUNTIME_TEST_PORT=" + portFile}}, func(f claude.Frame) {
		if f.Type == "result" {
			result <- struct{}{}
		}
	}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-result:
	case <-ctx.Done():
		t.Fatal("fake Claude did not finish reply")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(portFile)
		if err == nil {
			if port, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return client, port
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fake Claude's HTTP child did not start")
	return nil, 0
}
func TestClaudeRuntimeUsesDedicatedSessionProcessTrees(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	a, portA := startRuntimeClaude(t, ctx, dir)
	b, portB := startRuntimeClaude(t, ctx, dir)
	c := serverController(dir, "")
	c.ctx, c.cancel = context.WithCancel(ctx)
	t.Cleanup(func() { c.cancel() })
	c.claudeRuns = map[int]*claude.Client{1: a, 2: b}
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "claude-a"}, {ID: 2, Agent: agentClaude, ServerID: "claude-b"}})
	for _, tc := range []struct{ id, port, other int }{{1, portA, portB}, {2, portB, portA}} {
		work, err := c.prepareRuntime(c.threadRef(c.threadWithID(tc.id)), dir)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := work()
		if err != nil {
			t.Fatal(err)
		}
		result := raw.(conversationRuntime)
		if !result.View.TasksKnown || len(result.View.Tasks) != 1 || result.View.Tasks[0].Kind != "local_bash" || len(result.Services) != 1 || result.Services[0].Port != tc.port {
			t.Fatalf("wrong Claude session resources: %+v", result)
		}
		if !result.View.Capabilities.SessionRoot || result.View.Capabilities.NativePID {
			t.Fatal("Claude capability claims native PID support")
		}
		for _, endpoint := range result.View.Endpoints {
			if endpoint.Port == tc.other {
				t.Fatal("another Claude session's service leaked")
			}
		}
	}
	if !c.runtimes().busy() {
		t.Fatal("managed background resource did not delay upgrade")
	}
}
func TestRuntimeEndpointTokensRejectReusedPortsAndTampering(t *testing.T) {
	dir := t.TempDir()
	pid, port, stop := startRuntimeHTTP(t, dir)
	manager := newRuntimeManager(nil, filepath.Join(t.TempDir(), "runtime.json"))
	defer manager.Close()
	key := runtimeKey(testRef(agentCodex, "a"))
	manager.register(key, runtimeSource{Epoch: "e1", Caps: rt.Capabilities{RequiresHost: true}, Snapshot: func(context.Context) (runtimeSnapshot, error) {
		return runtimeSnapshot{Known: true, Tasks: []rt.Task{{ID: "opaque", Status: "running"}}, Roots: []rt.Root{{PID: pid, TaskID: "opaque"}}}, nil
	}})
	view := manager.refresh(context.Background(), key)
	result := runtimeResult(manager, key, view)
	if len(result.Services) != 1 || result.Services[0].Port != port {
		t.Fatalf("missing owned service: %+v", result)
	}
	service := result.Services[0]
	if err := manager.validateToken(context.Background(), service.RuntimeToken, service.Host, port); err != nil {
		t.Fatal(err)
	}
	if err := manager.validateToken(context.Background(), service.RuntimeToken+"bad", service.Host, port); err == nil {
		t.Fatal("tampered token accepted")
	}
	if err := manager.validateToken(context.Background(), service.RuntimeToken, service.Host, port+1); err == nil {
		t.Fatal("token allowed another port")
	}
	stop()
	reused, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer reused.Close()
	if err := manager.validateToken(context.Background(), service.RuntimeToken, service.Host, port); err == nil {
		t.Fatal("another process inherited a reused endpoint")
	}
}
func TestRuntimeUnavailableNativeDataNeverGuessesByDirectory(t *testing.T) {
	_, _, _ = startRuntimeHTTP(t, t.TempDir())
	manager := newRuntimeManager(nil, "")
	defer manager.Close()
	key := runtimeKey(testRef(agentCodex, "unknown"))
	manager.register(key, runtimeSource{Epoch: "e", Snapshot: func(context.Context) (runtimeSnapshot, error) {
		return runtimeSnapshot{}, errors.New("unsupported native method")
	}})
	view := manager.refresh(context.Background(), key)
	if view.TasksKnown || len(view.Endpoints) != 0 || len(view.Processes) != 0 || view.Notice == "" {
		t.Fatalf("missing evidence became guessed ownership: %+v", view)
	}
}
func TestRuntimeManagerCloseWaitsForOutstandingObservation(t *testing.T) {
	manager := newRuntimeManager(nil, "")
	key := runtimeKey(testRef(agentCodex, "close"))
	started := make(chan struct{})
	manager.register(key, runtimeSource{Epoch: "e", Snapshot: func(ctx context.Context) (runtimeSnapshot, error) {
		close(started)
		<-ctx.Done()
		return runtimeSnapshot{}, ctx.Err()
	}})
	manager.kick(key)
	<-started
	finished := make(chan struct{})
	go func() { manager.Close(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime observation did not stop with its host")
	}
	if manager.busy() {
		t.Fatal("closed runtime retained in-flight observer")
	}
}
func TestMCPRuntimeCannotBeRedirectedOrChooseLatestCaller(t *testing.T) {
	c := testController()
	c.run = func(f func()) { f() }
	c.cwd = t.TempDir()
	c.setThreads([]Thread{{ID: 1, ServerID: "a", Messages: []Message{{Kind: "mcpToolCall", Status: "inProgress", Item: &codex.Item{Server: "miya", Tool: "runtime_info"}}}}, {ID: 2, ServerID: "b", Messages: []Message{{Kind: "mcpToolCall", Status: "inProgress", Item: &codex.Item{Server: "miya", Tool: "runtime_info"}}}}})
	if _, err := c.mcpRuntime(mcpRequest{Reference: testRef(agentCodex, "a").URL(), Thread: 1}); err == nil {
		t.Fatal("ambiguous callers selected a thread")
	}
	c.setThreads(c.threads()[:1])
	value, err := c.mcpRuntime(mcpRequest{Reference: testRef(agentCodex, "b").URL(), Thread: 2})
	if err != nil {
		t.Fatal(err)
	}
	result, err := value.(deferred)()
	if err != nil {
		t.Fatal(err)
	}
	if result.(rt.View).Key.SessionID != "a" {
		t.Fatal("query arguments redirected runtime access")
	}
}

func TestProjectOverviewCombinesOwnedServicesAndUnownedCandidates(t *testing.T) {
	dir := t.TempDir()
	pid, ownedPort, _ := startRuntimeHTTP(t, dir)
	metadata, _ := json.Marshal(map[string]any{"resume-thread": []map[string]any{{"processId": "native", "itemId": "start", "osPid": pid, "command": "server"}}})
	t.Setenv("QUICKGUI_FIXTURE_BACKGROUND", string(metadata))
	c, _, _ := codexResumeFixture(t)
	c.run = func(f func()) { f() }
	work, err := c.prepareRuntime(c.threadRef(c.threadWithID(1)), c.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work(); err != nil {
		t.Fatal(err)
	}
	_, candidatePort, _ := startRuntimeHTTP(t, c.cwd)
	services, err := c.projectRuntimeServices(context.Background(), c.cwd)
	if err != nil {
		t.Fatal(err)
	}
	foundOwned, foundCandidate := false, false
	for _, service := range services {
		if service.Port == ownedPort {
			foundOwned = service.Owner != nil && service.RuntimeToken != ""
		}
		if service.Port == candidatePort {
			foundCandidate = service.Owner == nil && service.RuntimeToken == ""
		}
	}
	if !foundOwned || !foundCandidate {
		t.Fatalf("project overview conflated ownership: %+v", services)
	}
}
