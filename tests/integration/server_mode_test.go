//go:build !windows

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"miya/internal/controller"
	phoneproto "miya/internal/phone/protocol"
	projectfiles "miya/internal/project/files"
	projectgit "miya/internal/project/git"
	"miya/internal/remote"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	serviceserver "miya/internal/service/server"
	"miya/internal/service/workspaces"
	desktopui "miya/internal/ui"
	"miya/internal/webservice"
)

func TestServerPersistentIdleAndTerminalRestart(t *testing.T) {
	c := serverController(t.TempDir(), "")
	s := serviceserver.New(c, nil)
	s.Persistent = true
	s.IdleSince = time.Now().Add(-time.Hour)
	s.IdleTimeout = time.Nanosecond
	s.CheckIdle()
	select {
	case <-s.Quit:
		t.Fatal("permanent server exited without a client")
	default:
	}
	c.RemoteTerminals.Store(1)
	if _, err := s.Handle(nil, svcproto.Request{Op: "quitWhenIdle"}); err != nil {
		t.Fatal(err)
	}
	s.CheckIdle()
	select {
	case <-s.Quit:
		t.Fatal("safe restart ended an interactive terminal")
	default:
	}
	c.RemoteTerminals.Store(0)
	s.CheckIdle()
	select {
	case <-s.Quit:
	default:
		t.Fatal("idle server did not honor scheduled restart")
	}
}

func TestServerPrivateConnectionFiles(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	if err := os.WriteFile(controller.ConnectionFile(), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := controller.SaveServerConnection("test-capability"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(controller.ConnectionFile())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("pairing capability permissions", info, err)
	}
	if err := workspaces.SaveRemoteServer("bad-code"); err == nil {
		t.Fatal("invalid connection accepted")
	}
	if err := workspaces.SaveRemoteServer(""); err != nil {
		t.Fatal(err)
	}
	if workspaces.RemoteServerLink() != "" {
		t.Fatal("local mode did not clear remote target")
	}
	info, err = os.Stat(workspaces.RemoteServerPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("client capability permissions", info, err)
	}
}

func TestRemoteHostWorkspaceRPC(t *testing.T) {
	f := newServiceFixture(t)
	cl, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	root := f.C.Cwd
	file := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(file, []byte("server text"), 0640); err != nil {
		t.Fatal(err)
	}
	var entries []svcproto.HostEntry
	if err := cl.Call("host", svcproto.HostRequest{Action: "dir", Project: root}, &entries); err != nil || len(entries) < 1 {
		t.Fatal(entries, err)
	}
	var text string
	if err := cl.Call("host", svcproto.HostRequest{Action: "read", Project: root, Path: "hello.txt"}, &text); err != nil || text != "server text" {
		t.Fatal(text, err)
	}
	if err := cl.Call("host", svcproto.HostRequest{Action: "write", Project: root, Path: "hello.txt", Text: "saved remotely"}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "saved remotely" {
		t.Fatal(string(data), err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0640 {
		t.Fatal("remote save lost permissions")
	}
	var files []string
	if err := cl.Call("host", svcproto.HostRequest{Action: "index", Project: root}, &files); err != nil || !strings.Contains(strings.Join(files, "\n"), "hello.txt") {
		t.Fatal(files, err)
	}
	for _, r := range []svcproto.HostRequest{
		{Action: "read", Project: root, Path: "../secret"},
		{Action: "read", Project: "/unregistered", Path: "hello.txt"},
		{Action: "write", Project: root, Path: "hello.txt", Text: strings.Repeat("x", projectfiles.MaxEditableFile+1)},
	} {
		if err := cl.Call("host", r, nil); err == nil {
			t.Fatalf("invalid host request accepted: %s", r.Action)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call("host", svcproto.HostRequest{Action: "read", Project: root, Path: "escape"}, nil); err == nil {
		t.Fatal("project traversal through symlink accepted")
	}
}

func TestRemoteHostAttachmentsAndPreview(t *testing.T) {
	f := newServiceFixture(t)
	cl, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := cl.Call("host", svcproto.HostRequest{Action: "upload", Images: []remote.Image{{Name: "photo.png", Data: buf.Bytes()}}}, &paths); err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	var preview []byte
	source := (&url.URL{Scheme: "file", Path: paths[0]}).String()
	if err := cl.Call("host", svcproto.HostRequest{Action: "preview", Path: source}, &preview); err != nil || len(preview) == 0 {
		t.Fatal("preview unavailable", err)
	}
	c := controller.NewMirrorController()
	c.Cwd, c.Remote, c.Mirror.RemoteHost = f.C.Cwd, cl, true
	c.SetActive(7)
	c.RestoreDraft(7, "retry", paths)
	got := c.Attachments()
	if len(got) != 1 || got[0] != "miya-server:"+paths[0] {
		t.Fatal("returned attachment lost server identity", got)
	}
	resolved, err := c.UploadRemoteAttachments(got)
	if err != nil || len(resolved) != 1 || resolved[0] != paths[0] {
		t.Fatal("server attachment re-uploaded", resolved, err)
	}
	document := filepath.Join(t.TempDir(), "brief.txt")
	if err := os.WriteFile(document, []byte("attached document"), 0600); err != nil {
		t.Fatal(err)
	}
	uploaded, err := c.UploadRemoteAttachments([]string{document})
	if err != nil || len(uploaded) != 1 || uploaded[0] == document {
		t.Fatal("ordinary document did not cross host boundary", uploaded, err)
	}
	content, err := os.ReadFile(uploaded[0])
	if err != nil || string(content) != "attached document" {
		t.Fatal("document upload changed contents", err)
	}
	info, _ := os.Stat(uploaded[0])
	if info.Mode().Perm() != 0600 {
		t.Fatal("document upload permissions")
	}
	c.Cancel()
}

func TestRemoteHostDeletedGitFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "deleted.txt"), "old\n")
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	c := serverController(root, "")
	snap, err := projectgit.CollectDiff(context.Background(), root, projectgit.SourceUncommitted)
	if err != nil || len(snap.Files) != 1 {
		t.Fatal(snap, err)
	}
	work, err := c.PrepareHost(svcproto.HostRequest{Action: "sides", Project: root, Snapshot: snap, File: snap.Files[0]})
	if err != nil {
		t.Fatal(err)
	}
	result, err := work()
	if err != nil || result.(svcproto.HostSides).Old != "old\n" || result.(svcproto.HostSides).New != "" {
		t.Fatal(result, err)
	}
}

func TestRemoteBrowserServerTargets(t *testing.T) {
	for _, raw := range []string{"http://localhost:3000/docs?q=yes", "http://127.0.0.1:5173/", "http://[::1]:8080/"} {
		s, u, proxy, err := desktopui.RemoteBrowserService(raw, webservice.Service{})
		if err != nil || !proxy || s.Port == 0 || u == nil {
			t.Fatal(raw, s, err)
		}
	}
	if _, _, proxy, err := desktopui.RemoteBrowserService("https://example.com/", webservice.Service{}); proxy || err != nil {
		t.Fatal("public website was redirected to server", err)
	}
	if _, _, _, err := desktopui.RemoteBrowserService("https://localhost:3000/", webservice.Service{}); err == nil {
		t.Fatal("unsupported localhost TLS silently used client network")
	}
}

// This test uses temporary identities and synthetic data over real relays.
func TestServerTailcatEndToEnd(t *testing.T) {
	if os.Getenv("MIYA_SERVER_E2E") != "1" {
		t.Skip("set MIYA_SERVER_E2E=1 for public relay integration")
	}
	f := newServiceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	root := f.C.Cwd
	if err := os.WriteFile(filepath.Join(root, "remote.txt"), []byte("tailcat server file"), 0600); err != nil {
		t.Fatal(err)
	}
	bridge, err := remote.Start(ctx, remote.Options{
		StateDir: t.TempDir(), Files: f.C.FileServer(), Serve: f.C.ServePhone,
		Service:  func(ctx context.Context, wire *remote.Conn) { f.srv.HandleConn(&serviceserver.Peer{Wire: wire}) },
		Terminal: f.C.ServeRemoteTerminal,
	})
	if err != nil {
		t.Fatal("start Tailcat", err)
	}
	defer bridge.Close()
	cl, state, err := workspaces.ConnectRemoteService(bridge.Link())
	if err != nil {
		t.Fatal("desktop connect", err)
	}
	defer cl.Close()
	if state.Cwd != root {
		t.Fatal("desktop received another workspace")
	}
	var text string
	if err := cl.Call("host", svcproto.HostRequest{Action: "read", Project: root, Path: "remote.txt"}, &text); err != nil || text != "tailcat server file" {
		t.Fatal("desktop remote read", text, err)
	}
	// A deferred disk read must not prevent heartbeats/stop/approvals on
	// this same desktop connection. A FIFO supplies a deterministic wait.
	pipe := filepath.Join(root, "slow-read")
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	defer func() { go func() { _ = os.WriteFile(pipe, []byte("released"), 0600) }() }()
	args, _ := json.Marshal(svcproto.HostRequest{Action: "read", Project: root, Path: "slow-read"})
	if err := cl.Wire().Send(svcproto.Request{ID: 1000000, Op: "host", Args: args}); err != nil {
		t.Fatal(err)
	}
	heartbeat := make(chan error, 1)
	go func() { heartbeat <- cl.Call("hello", nil, nil) }()
	select {
	case err := <-heartbeat:
		if err != nil {
			t.Fatal("heartbeat during deferred request", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("deferred request blocked desktop control")
	}
	phone, err := remote.Connect(ctx, bridge.Link())
	if err != nil {
		t.Fatal("phone connect", err)
	}
	defer phone.Close()
	_ = phone.SetReadDeadline(time.Now().Add(20 * time.Second))
	var hello remote.Frame
	if err := phone.Receive(&hello); err != nil || hello.Type != "hello" || hello.Version != remote.ProtocolVersion {
		t.Fatal("phone greeting", hello.Type, err)
	}
	phone.DecompressReceives()
	var frame remote.Frame
	if err := phone.Receive(&frame); err != nil || frame.Type != "state" {
		t.Fatal("phone state", frame.Type, err)
	}
	var phoneState phoneproto.State
	if err := json.Unmarshal(frame.Data, &phoneState); err != nil || len(phoneState.Projects) == 0 {
		t.Fatal("phone projects", err)
	}
	term, err := svcclient.OpenTerminal(cl, root)
	if err != nil {
		t.Fatal("terminal open", err)
	}
	defer term.Close()
	if _, err := io.WriteString(term, "printf '%s%s\\n' miya-server- terminal-ok; pwd\r"); err != nil {
		t.Fatal(err)
	}
	output := make(chan string, 1)
	go func() {
		var all strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			all.Write(buf[:n])
			if err != nil || strings.Contains(all.String(), "miya-server-terminal-ok") && strings.Contains(all.String(), root) {
				output <- all.String()
				return
			}
		}
	}()
	select {
	case result := <-output:
		if !strings.Contains(result, "miya-server-terminal-ok") || !strings.Contains(result, root) {
			t.Fatalf("remote shell did not execute in project: %q", result)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("remote shell timed out")
	}
	cl.Close()
	if _, err := cl.Wire().DialFiles(ctx); err == nil {
		t.Fatal("closed desktop still opened file transport")
	}
}

// Optional deployment probe against a task-owned Linux instance. The secret
// stays in a local file and is never printed by a test failure.
func TestServerLinuxDeployment(t *testing.T) {
	path := os.Getenv("MIYA_SERVER_QA_LINK_FILE")
	if path == "" {
		t.Skip("set MIYA_SERVER_QA_LINK_FILE for a disposable Linux server")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read QA link", err)
	}
	t.Setenv("MIYA_DIR", t.TempDir())
	cl, state, err := workspaces.ConnectRemoteService(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("Linux desktop connect", err)
	}
	defer cl.Close()
	var hello svcproto.Hello
	if err := cl.Call("hello", nil, &hello); err != nil || !hello.Remote {
		t.Fatal("not a permanent Linux server", err)
	}
	request := svcproto.HostRequest{Action: "write", Project: state.Cwd, Path: "miya-linux-qa.txt", Text: "linux server round trip"}
	if err := cl.Call("host", request, nil); err != nil {
		t.Fatal("Linux remote save", err)
	}
	var text string
	request.Action = "read"
	if err := cl.Call("host", request, &text); err != nil || text != request.Text {
		t.Fatal("Linux remote read", text, err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) { return cl.Wire().DialFiles(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	response, err := client.Get("http://server/" + phoneproto.ProjectFileID(state.Cwd) + "/miya-linux-qa.txt")
	if err != nil {
		t.Fatal("Linux file stream", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != request.Text {
		t.Fatal("Linux file download", response.StatusCode, err)
	}
	term, err := svcclient.OpenTerminal(cl, state.Cwd)
	if err != nil {
		t.Fatal("Linux shell", err)
	}
	defer term.Close()
	if _, err := io.WriteString(term, "printf '%s%s\\n' linux- pty-ok; uname -s\r"); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	buffer := make([]byte, 4096)
	for !strings.Contains(output.String(), "linux-pty-ok") || !strings.Contains(output.String(), "Linux") {
		n, err := term.Read(buffer)
		output.Write(buffer[:n])
		if err != nil {
			t.Fatal("Linux PTY output", err)
		}
	}
	phone, err := remote.Connect(context.Background(), strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("Linux phone connect", err)
	}
	defer phone.Close()
	_ = phone.SetReadDeadline(time.Now().Add(20 * time.Second))
	var frame remote.Frame
	if err := phone.Receive(&frame); err != nil || frame.Type != "hello" {
		t.Fatal("Linux phone greeting", err)
	}
	phone.DecompressReceives()
	if err := phone.Receive(&frame); err != nil || frame.Type != "state" {
		t.Fatal("Linux phone state", err)
	}
}
