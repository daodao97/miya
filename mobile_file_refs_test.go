package main

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPhoneFileReferencesSearchProject(t *testing.T) {
	root, handler := fileServerFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/memory.md"), []byte("memory"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private-memory.md")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "memory-outside.md")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	choices, err := fetchPhoneFileReferences(context.Background(), dial, root, "memory")
	if err != nil || len(choices) != 1 || choices[0].Name != "docs/memory.md" {
		t.Fatalf("file candidates: %+v, %v", choices, err)
	}
	choices, err = fetchPhoneFileReferences(context.Background(), dial, root, "missing")
	if err != nil || len(choices) != 0 {
		t.Fatalf("empty file search: %+v, %v", choices, err)
	}
	if _, err := fetchPhoneFileReferences(context.Background(), dial, filepath.Dir(outside), "memory"); err == nil {
		t.Fatal("search accepted a project not shared by the desktop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchPhoneFileReferences(ctx, dial, root, "memory"); err == nil {
		t.Fatal("canceled search succeeded")
	}
}

func TestMobileComposerOffersFilesAlongsideConversations(t *testing.T) {
	m := testMobileApp()
	m.state.FileReferences = true
	m.state.Projects[0].Threads = append(m.state.Projects[0].Threads, Thread{ID: 3, ServerID: "memory-chat", Agent: agentCodex, Title: "memory 设计"})
	th := m.state.Projects[0].Threads[0]
	th.HistoryLoaded = true
	m.openThread(th)
	m.showThread(th)
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	tt.Type("查看 @memory")
	m.fileRefs = &mobileFileReferences{project: "/a", query: "memory", supported: true, choices: []composerChoice{{Name: "docs/memory notes.md"}}}
	tt.Frame()
	if !tt.HasText("Referenced conversation") || !tt.HasText("memory 设计") || !tt.HasText("Reference file") || !tt.HasText("docs/memory notes.md") {
		t.Fatalf("missing reference choices: %q", tt.Texts())
	}
	saveMobileNativeSnapshot(t, tt, "composer-file-references")
	if err := tt.Click("docs/memory notes.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.draft != `查看 @"docs/memory notes.md" ` || tt.HasText("Reference file") {
		t.Fatalf("file choice did not replace token or close menu: %q", m.draft)
	}
	if m.fileRefs != nil {
		t.Fatal("closed menu retained file search")
	}
}

func TestMobileFileReferencesWithoutConversationSource(t *testing.T) {
	m := testMobileApp()
	m.state.ReferenceSource, m.state.FileReferences = "", true
	m.draft = "@memory"
	m.fileRefs = &mobileFileReferences{project: "/a", query: "memory", supported: true, choices: []composerChoice{{Name: "docs/memory.md"}}}
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	if !tt.HasText("docs/memory.md") || tt.HasText("Referenced conversation") {
		t.Fatalf("file search depends on conversation references: %q", tt.Texts())
	}
	if err := tt.Click("docs/memory.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(m.draft, "@docs/memory.md ") {
		t.Fatalf("wrong file reference: %q", m.draft)
	}
}

func TestMobileFileReferencesCancelWhenProjectOrQueryChanges(t *testing.T) {
	for _, change := range []struct{ project, query string }{{"/b", "memory"}, {"/a", "other"}} {
		m := testMobileApp()
		ctx, cancel := context.WithCancel(context.Background())
		m.fileRefs = &mobileFileReferences{project: "/a", query: "memory", cancel: cancel, loading: true, choices: []composerChoice{{Name: "docs/memory.md"}}}
		state := m.searchFileReferences(change.project, change.query)
		if ctx.Err() == nil || state.loading || len(state.choices) != 0 || state.project != change.project || state.query != change.query {
			t.Fatalf("old search survived project/query change: %+v", state)
		}
	}
}
