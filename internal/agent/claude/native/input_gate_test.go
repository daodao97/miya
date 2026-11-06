package claude

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestBeforeInputCanRejectAfterNativeInitialization(t *testing.T) {
	t.Setenv("QUICKGUI_CLAUDE_TEST_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rejected := errors.New("owner cancelled while initializing")
	done := make(chan error, 1)
	frames := make(chan Frame, 10)
	_, err = Start(ctx, Options{Binary: exe, Cwd: t.TempDir(), BeforeInput: func(context.Context) error { return rejected }}, func(f Frame) { frames <- f }, func(e error) { done <- e })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, rejected) {
			t.Fatalf("lost input rejection: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("native process did not exit after rejected input")
	}
	if len(frames) != 0 {
		t.Fatal("native model received input despite owner rejection")
	}
}
