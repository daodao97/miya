package login

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (context.Context, Provider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	path, err := filepath.Abs("../../../../testdata/account-login-cli.py")
	if err != nil {
		t.Fatal(err)
	}
	return ctx, Provider{Binary: path, Home: t.TempDir()}
}

func TestManualNativeLoginUsesSameProcessAndVerifiesSavedAccount(t *testing.T) {
	ctx, p := fixture(t)
	identity, err := p.Check(ctx)
	if err != nil || identity.LoggedIn {
		t.Fatalf("signed-out result: %+v %v", identity, err)
	}
	f, challenge, err := p.Start(ctx, "browser")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !strings.Contains(challenge.URL, "state=fixture-state") {
		t.Fatal("no native state")
	}
	if f.Submit(ctx, "fixture-code#wrong-state") == nil {
		t.Fatal("accepted stale authorization code")
	}
	done := make(chan error, 1)
	go func() { done <- f.Wait(ctx) }()
	// Wait and Submit run concurrently. The native waiter was registered
	// before Start exposed the URL, so callback submission cannot overtake it.
	if err := f.Submit(ctx, "fixture-code#fixture-state"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("native completion deadlocked")
	}
	identity, err = p.Check(ctx)
	if err != nil || !identity.LoggedIn || identity.Email != filepath.Base(p.Home)+"@example.invalid" {
		t.Fatalf("target account not verified: %+v %v", identity, err)
	}
}

func TestOfficialClaudeLoginSmoke(t *testing.T) {
	if os.Getenv("MIYA_LOGIN_SMOKE") != "1" {
		t.Skip("opt-in official CLI link generation; no code is authorized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := Provider{Home: t.TempDir()}
	f, challenge, err := p.Start(ctx, "browser")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !strings.Contains(challenge.URL, "code=true") {
		t.Fatal("official CLI returned no manual link")
	}
	native := f.(*flow)
	if err := native.call(ctx, "probe", map[string]any{"subtype": "mcp_status"}, nil); err != nil {
		t.Fatal(err)
	}
	// The serial input loop has processed a later read-only request. An
	// unsupported completion control would already have answered with error.
	select {
	case r := <-native.completion:
		t.Fatalf("official completion waiter stopped before authorization: failed=%v", r.failed)
	default:
	}
}

func TestNativeFailureDoesNotExposeCodesOrAssumeExistingCredentialsSucceeded(t *testing.T) {
	ctx, p := fixture(t)
	t.Setenv("LOGIN_FIXTURE_MODE", "failed-code")
	f, _, err := p.Start(ctx, "browser")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Submit(ctx, "fixture-code#fixture-state"); err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(ctx); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatal("native failure was lost or exposed protocol output", err)
	}
}
