package integration

import (
	"context"
	"miya/internal/chat/model"
	mobile "miya/internal/phone/client"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
	choices, err := mobile.FetchPhoneFileReferences(context.Background(), dial, root, "memory")
	if err != nil || len(choices) != 1 || choices[0].Name != "docs/memory.md" {
		t.Fatalf("file candidates: %+v, %v", choices, err)
	}
	choices, err = mobile.FetchPhoneFileReferences(context.Background(), dial, root, "missing")
	if err != nil || len(choices) != 0 {
		t.Fatalf("empty file search: %+v, %v", choices, err)
	}
	if _, err := mobile.FetchPhoneFileReferences(context.Background(), dial, filepath.Dir(outside), "memory"); err == nil {
		t.Fatal("search accepted a project not shared by the desktop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mobile.FetchPhoneFileReferences(ctx, dial, root, "memory"); err == nil {
		t.Fatal("canceled search succeeded")
	}
}

func TestMobileFileReferencesCancelWhenProjectOrQueryChanges(t *testing.T) {
	for _, change := range []struct{ project, query string }{{"/b", "memory"}, {"/a", "other"}} {
		m := testPhoneClient()
		ctx, cancel := context.WithCancel(context.Background())
		m.FileRefs = &mobile.MobileFileReferences{Project: "/a", Query: "memory", Cancel: cancel, Loading: true, Choices: []model.ComposerChoice{{Name: "docs/memory.md"}}}
		state := m.SearchFileReferences(change.project, change.query)
		if ctx.Err() == nil || state.Loading || len(state.Choices) != 0 || state.Project != change.project || state.Query != change.query {
			t.Fatalf("old search survived project/query change: %+v", state)
		}
	}
}
