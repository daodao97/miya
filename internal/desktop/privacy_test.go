package desktop

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// A real child process exercises pipes, startup failure and cancellation without
// ever changing the physical display in ordinary tests.
func TestPrivacyProcessFixture(t *testing.T) {
	mode := os.Getenv("MIYA_PRIVACY_PROCESS_TEST")
	if mode == "" {
		return
	}
	if mode == "reject" {
		os.Exit(1)
	}
	if mode == "slow" {
		time.Sleep(time.Minute)
		os.Exit(1)
	}
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func privacyFixture(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPrivacyProcessFixture$")
	cmd.Env = append(os.Environ(), "MIYA_PRIVACY_PROCESS_TEST="+mode)
	return cmd
}

func TestPrivacyProcessClosesAndCancels(t *testing.T) {
	for _, end := range []string{"close", "cancel", "child exit"} {
		t.Run(end, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := privacyFixture(t, "ready")
			p, err := startPrivacyProcess(ctx, cmd)
			if err != nil {
				t.Fatal(err)
			}
			switch end {
			case "close":
				_ = p.Close()
			case "cancel":
				cancel()
			case "child exit":
				_ = cmd.Process.Kill()
			}
			select {
			case <-p.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("helper did not finish")
			}
			_ = p.Close() // repeated cleanup is harmless
		})
	}
}

func TestPrivacyProcessRejectsFailureAndCancelledStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := startPrivacyProcess(ctx, privacyFixture(t, "ready")); err == nil || p != nil {
		t.Fatal("cancelled startup succeeded")
	}
	if p, err := startPrivacyProcess(context.Background(), privacyFixture(t, "reject")); err == nil || p != nil {
		t.Fatal("failed startup succeeded")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if p, err := startPrivacyProcess(ctx, privacyFixture(t, "slow")); err == nil || p != nil {
		t.Fatal("slow startup succeeded")
	}
}

func TestPrivacyProcessHasOnlyOneOwner(t *testing.T) {
	p, err := startPrivacyProcess(context.Background(), privacyFixture(t, "ready"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if other, err := startPrivacyProcess(context.Background(), privacyFixture(t, "ready")); err == nil || other != nil {
		t.Fatal("two helpers could overwrite each other's original gamma tables")
	}
	select {
	case <-p.Done():
		t.Fatal("second startup stopped the active owner")
	default:
	}
	_ = p.Close()
	next, err := startPrivacyProcess(context.Background(), privacyFixture(t, "ready"))
	if err != nil {
		t.Fatal("helper cannot reopen after restoration", err)
	}
	_ = next.Close()
}
