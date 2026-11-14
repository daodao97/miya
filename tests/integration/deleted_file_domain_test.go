//go:build !mygo_ui

package integration

import (
	"context"
	projectgit "miya/internal/project/git"
	svcproto "miya/internal/service/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestNSGoRemoteHostDeletedGitFile(t *testing.T) {
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
