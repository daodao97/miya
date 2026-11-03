package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectWebListenersAndDirectories(t *testing.T) {
	data := "p10\x00cnode\x00\nf4\x00n*:5173\x00\nf5\x00n[::1]:5173\x00\np20\x00cpython\x00\nf7\x00n127.0.0.1:8000\x00\nf8\x00n192.168.1.5:9000\x00\nf9\x00ninvalid\x00\np30\x00cgo\x00\nf5\x00tIPv6\x00n*:8080\x00\nf6\x00tIPv4\x00n127.0.0.2:9000\x00\n"
	processes := parseWebListeners([]byte(data))
	want := []webListenerProcess{{pid: 10, name: "node", ports: []int{5173, 5173}, hosts: map[int]string{5173: "127.0.0.1"}}, {pid: 20, name: "python", ports: []int{8000}, hosts: map[int]string{8000: "127.0.0.1"}}, {pid: 30, name: "go", ports: []int{8080}, hosts: map[int]string{8080: "::1"}}}
	if !reflect.DeepEqual(processes, want) {
		t.Fatalf("listeners: %+v", processes)
	}
	dirs := parseWebProcessDirectories([]byte("p10\x00\nfcwd\x00n/work/项目 with spaces\x00\np20\x00\nfcwd\x00n/work/other\x00\n"))
	if dirs[10] != "/work/项目 with spaces" || dirs[20] != "/work/other" {
		t.Fatalf("cwd: %v", dirs)
	}
}

func TestProjectWebCandidatesStayInsideProject(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	child := filepath.Join(root, "frontend")
	sibling := filepath.Join(parent, "project-other")
	for _, path := range []string{child, sibling} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	escape := filepath.Join(root, "external")
	if err := os.Symlink(sibling, escape); err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(root)
	processes := []webListenerProcess{
		{pid: 1, name: "node", cwd: root, ports: []int{5173, 5173}},
		{pid: 2, name: "api", cwd: child, ports: []int{3000}},
		{pid: 3, name: "other", cwd: sibling, ports: []int{8080}},
		{pid: 4, name: "parent", cwd: parent, ports: []int{9000}},
		{pid: 5, name: "outside symlink", cwd: escape, ports: []int{9999}},
		{pid: 6, name: "unknown cwd", ports: []int{80}},
	}
	want := []phoneWebService{{Port: 3000, Process: "api"}, {Port: 5173, Process: "node"}}
	if got := projectWebCandidates(canonical, processes); !reflect.DeepEqual(got, want) {
		t.Fatalf("project filtering: %+v", got)
	}
}

func TestProjectWebProbeUsesHeadAndDoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("probe used %s", r.Method)
		}
		http.Redirect(w, r, target.URL, 302)
	}))
	defer app.Close()
	u, _ := url.Parse(app.URL)
	port, _ := strconv.Atoi(u.Port())
	binary, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer binary.Close()
	go func() {
		for {
			conn, err := binary.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("not HTTP\n"))
			conn.Close()
		}
	}()
	rawPort := binary.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	services, err := probeProjectWebServices(ctx, []phoneWebService{{Port: port, Process: "app"}, {Port: rawPort, Process: "database"}})
	if err != nil || len(services) != 1 || services[0].Port != port {
		t.Fatalf("HTTP detection: %+v, %v", services, err)
	}
	if targetHits.Load() != 0 {
		t.Fatal("probe followed a redirect")
	}
	cancel()
	if _, err := probeProjectWebServices(ctx, []phoneWebService{{Port: port}}); err == nil {
		t.Fatal("canceled scan succeeded")
	}
}

// This process owns a listener with a controlled cwd, for the real lsof test.
func TestProjectWebDiscoveryHelper(t *testing.T) {
	mode := os.Getenv("MIYA_WEB_DISCOVERY_HELPER")
	if mode == "" {
		return
	}
	host := os.Getenv("MIYA_WEB_DISCOVERY_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(host, "0"))
	if err != nil {
		os.Exit(2)
	}
	fmt.Println(listener.Addr().(*net.TCPAddr).Port)
	if mode == "http" {
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	} else {
		for {
			conn, err := listener.Accept()
			if err != nil {
				break
			}
			conn.Close()
		}
	}
	os.Exit(0)
}

func startProjectWebHelper(t *testing.T, directory, mode string) int {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestProjectWebDiscoveryHelper$")
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "MIYA_WEB_DISCOVERY_HELPER="+mode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestDiscoverProjectWebServicesWithRealListeners(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof unavailable")
	}
	project := t.TempDir()
	child := filepath.Join(project, "前端 with spaces")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	rootPort := startProjectWebHelper(t, project, "http")
	childPort := startProjectWebHelper(t, child, "http")
	_ = startProjectWebHelper(t, t.TempDir(), "http")
	_ = startProjectWebHelper(t, project, "tcp")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	services, err := discoverProjectWebServices(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	ports := map[int]bool{}
	for _, service := range services {
		ports[service.Port] = true
	}
	if len(services) != 2 || !ports[rootPort] || !ports[childPort] {
		t.Fatalf("wrong project services: %+v", services)
	}
}

func TestProjectWebServicesEndpointAndPhoneFetch(t *testing.T) {
	root, fixture := fileServerFixture(t)
	handler := fixture.(desktopFileServer)
	calls := 0
	handler.webServices = func(ctx context.Context, project string) ([]phoneWebService, error) {
		calls++
		if project != root {
			t.Fatalf("wrong project %q", project)
		}
		return []phoneWebService{{Port: 5173, Process: "node"}}, nil
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	services, err := fetchPhoneWebServices(context.Background(), dial, root)
	if err != nil || len(services) != 1 || services[0].Port != 5173 {
		t.Fatalf("phone discovery: %+v %v", services, err)
	}
	if _, err := fetchPhoneWebServices(context.Background(), dial, t.TempDir()); err == nil {
		t.Fatal("unshared project accepted")
	}
	if w := fileRequest(t, handler, "POST", "/"+projectFileID(root)+"/"+projectWebServicesPath, nil); w.Code != 405 {
		t.Fatal("discovery accepted a write")
	}
	if calls != 1 {
		t.Fatalf("discovery called %d times", calls)
	}
}

func TestProjectWebServiceKeepsIPv6ListenerOnSharedPort(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "other project") }))
	defer other.Close()
	u, _ := url.Parse(other.URL)
	port, _ := strconv.Atoi(u.Port())
	listener, err := net.Listen("tcp6", net.JoinHostPort("::1", u.Port()))
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	app := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Miya-Web-Host") != "" {
			t.Error("private routing header leaked to app")
		}
		fmt.Fprint(w, "current project")
	}))
	app.Listener = listener
	app.Start()
	defer app.Close()
	bridge := httptest.NewServer(desktopWebServer())
	defer bridge.Close()
	p := &phoneFileProxy{webHost: "::1"}
	if err := p.startProxy(desktopWebPrefix+u.Port(), "http://localhost:"+u.Port(), func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", bridge.Listener.Addr().String())
	}); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	response, err := client.Get(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "current project" {
		t.Fatalf("wrong service on shared port: %q", body)
	}
	request, _ := http.NewRequest("GET", response.Request.URL.String(), nil)
	request.Header.Set("X-Miya-Web-Host", "127.0.0.1")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "current project" {
		t.Fatal("page could override selected service address")
	}
	if got, err := probeProjectWebServices(context.Background(), []phoneWebService{{Port: port, Host: "::1"}}); err != nil || len(got) != 1 {
		t.Fatalf("IPv6 discovery failed: %+v %v", got, err)
	}
	if w := fileRequest(t, desktopWebServer(), "GET", desktopWebPrefix+u.Port()+"/", map[string]string{"X-Miya-Web-Host": "example.com"}); w.Code != 400 {
		t.Fatal("non-loopback route accepted")
	}
}
