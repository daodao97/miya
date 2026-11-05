package main

import (
	"context"
	"fmt"
	"io"
	"miya/internal/webservice"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestProjectWebServicesEndpointAndPhoneFetch(t *testing.T) {
	root, fixture := fileServerFixture(t)
	handler := fixture.(desktopFileServer)
	calls := 0
	handler.webServices = func(ctx context.Context, project string) ([]webservice.Service, error) {
		calls++
		if project != root {
			t.Fatalf("wrong project %q", project)
		}
		return []webservice.Service{{Port: 5173, Process: "node"}}, nil
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
	if w := fileRequest(t, handler, "POST", "/"+projectFileID(root)+"/"+webservice.ProjectFile, nil); w.Code != 405 {
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
	bridge := httptest.NewServer(webservice.Server())
	defer bridge.Close()
	p := &phoneFileProxy{webHost: "::1"}
	if err := p.startProxy(webservice.Prefix+u.Port(), "http://localhost:"+u.Port(), func(ctx context.Context) (net.Conn, error) {
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
	if got, err := webservice.Probe(context.Background(), []webservice.Service{{Port: port, Host: "::1"}}); err != nil || len(got) != 1 {
		t.Fatalf("IPv6 discovery failed: %+v %v", got, err)
	}
	if w := fileRequest(t, webservice.Server(), "GET", webservice.Prefix+u.Port()+"/", map[string]string{"X-Miya-Web-Host": "example.com"}); w.Code != 400 {
		t.Fatal("non-loopback route accepted")
	}
}

// TestProjectWebDiscoveryHelper is a child process for tests that re-run this
// package's test binary (runtime_manager_test.go). internal/webservice has
// the same entry point for its own discovery tests.
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
