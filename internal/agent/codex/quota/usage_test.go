package quota

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"miya/internal/agent/quota/testcli"
)

func TestMain(m *testing.M) { testcli.Main(m) }
func TestCodexReadsOnlyAccountAndGeneralLimits(t *testing.T) {
	r, err := readCodex(context.Background(), testcli.Helper(t, "codex"), t.TempDir())
	plan, windows := r.Plan, r.Windows
	if err != nil || plan != "pro" || len(windows) != 2 {
		t.Fatalf("%s %+v %v", plan, windows, err)
	}
	if windows[0].Name != "5 hours" || windows[0].Used != 17 || windows[0].Reset == "" || windows[1].Name != "7 days" {
		t.Fatalf("wrong windows: %+v", windows)
	}
	if windows[0].DurationMins != 300 || windows[0].ResetsAt != 1900000000 || windows[1].DurationMins != 10080 || windows[1].ResetsAt != 0 {
		t.Fatalf("lost structured window times: %+v", windows)
	}
}

func TestCodexAPIKeyIsNotZeroQuota(t *testing.T) {
	r, err := readCodex(context.Background(), testcli.Helper(t, "api"), t.TempDir())
	windows := r.Windows
	if err == nil || len(windows) > 0 {
		t.Fatalf("API account: %+v %v", windows, err)
	}
}

func TestOwnedReaderTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := readCodex(ctx, testcli.Helper(t, "timeout"), t.TempDir())
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("unbounded reader: %v", err)
	}
}

func TestReaderCancellationIncludesOwnedLauncherChild(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix process groups")
	}
	root := t.TempDir()
	ready, survived := filepath.Join(root, "ready"), filepath.Join(root, "survived")
	t.Setenv("USAGE_TREE_READY", ready)
	t.Setenv("USAGE_TREE_SURVIVED", survived)
	binary := testcli.Helper(t, "tree")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _, _ = readCodex(ctx, binary, root); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned launcher child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reader cancellation hung")
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(survived); err == nil {
		t.Fatal("npm-style launcher child outlived its canceled reader")
	}
}
func TestAbsentWindow(t *testing.T) {
	if windows := codexWindows(codexLimits{Primary: &codexWindow{Mins: 300}}); len(windows) != 0 {
		t.Fatal("missing percent became zero")
	}
}
