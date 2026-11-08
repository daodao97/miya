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

func TestDeviceLoginOwnsNativeAttemptAndReadsTargetAccount(t *testing.T) {
	ctx, p := fixture(t)
	identity, err := p.Check(ctx)
	if err != nil || identity.LoggedIn {
		t.Fatalf("signed-out native state: %+v %v", identity, err)
	}
	f, challenge, err := p.Start(ctx, "device")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if challenge.URL != "https://auth.openai.com/codex/device" || challenge.UserCode != "ABCD-1234" {
		t.Fatalf("wrong challenge: %+v", challenge)
	}
	if f.Submit(ctx, "anything") == nil {
		t.Fatal("device flow accepted a pasted authorization code")
	}
	if err := f.(*flow).client.Call(ctx, "test/complete", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	identity, err = p.Check(ctx)
	if err != nil || !identity.LoggedIn || identity.Email != filepath.Base(p.Home)+"@example.invalid" {
		t.Fatalf("target account was not read: %+v %v", identity, err)
	}
	f.Close()
	if _, err := os.Stat(filepath.Join(p.Home, "fixture-cancelled")); err != nil {
		t.Fatal("native login cancellation was not sent", err)
	}
}

func TestNativeDeviceErrorsDoNotExposeProtocolSecrets(t *testing.T) {
	ctx, p := fixture(t)
	t.Setenv("LOGIN_FIXTURE_MODE", "failed-start")
	_, _, err := p.Start(ctx, "device")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatal("unsafe or missing native error", err)
	}
}

func TestOfficialDeviceLoginSmoke(t *testing.T) {
	if os.Getenv("MIYA_LOGIN_SMOKE") != "1" {
		t.Skip("opt-in official CLI link generation; no code is authorized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := Provider{Home: t.TempDir()}
	f, challenge, err := p.Start(ctx, "device")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if challenge.UserCode == "" || challenge.URL == "" {
		t.Fatal("official CLI returned no challenge")
	}
}

func TestDeviceLoginUsesConnectedAppServerWithoutClosingIt(t *testing.T) {
	ctx, p := fixture(t)
	cl, dir, err := p.client(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	defer closeOwned(cl)
	connected := Provider{Client: cl}
	f, _, err := connected.Start(ctx, "device")
	if err != nil {
		t.Fatal(err)
	}
	if f.(*flow).client != cl || f.(*flow).owned {
		t.Fatal("login bypassed the running app-server")
	}
	if err := cl.Call(ctx, "test/complete", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	f.Close()
	identity, err := connected.Check(ctx)
	if err != nil || !identity.LoggedIn {
		t.Fatalf("running app-server did not observe login: %+v %v", identity, err)
	}
	select {
	case <-cl.Done():
		t.Fatal("login cleanup closed the shared app-server")
	default:
	}
}
