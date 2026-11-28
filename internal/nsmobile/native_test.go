package nsmobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	phonecache "miya/internal/phone/cache"
	phoneclient "miya/internal/phone/client"
	"miya/internal/remote"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"github.com/tailscale/tailcat"
)

func nativeLink() string {
	key := tailcat.NewPrivateKey()
	key.Public.RegionID = 1
	return remote.Link(key.Public.Addr())
}

func TestPhoneReconnectsWhenReturningThroughInactive(t *testing.T) {
	if nsgotest.Target() != "" {
		t.Skip("native test hosts use the system lifecycle")
	}
	link := nativeLink()
	type attempt struct {
		ctx  context.Context
		link string
	}
	dials := make(chan attempt, 4)
	app, m, _ := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		m.Link = link
		m.PrepareNetwork = func(context.Context, string) error { return nil }
		m.DialDesktop = func(ctx context.Context, raw string) (*remote.Conn, error) {
			dials <- attempt{ctx: ctx, link: raw}
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	t.Cleanup(func() { app.Post(m.Close); app.Settle() })
	awaitDial := func() attempt {
		t.Helper()
		var dial attempt
		select {
		case dial = <-dials:
		case <-time.After(time.Second):
			t.Fatal("the phone did not start dialing the paired desktop")
		}
		if dial.link != link {
			t.Fatal("the phone dialed a different desktop")
		}
		return dial
	}
	lifecycle := func(state core.Lifecycle) {
		app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Locale: "en", Lifecycle: state})
		app.Settle()
	}
	changePhone(app, m, func() { m.Connect(link); m.Draft = "keep this draft" })
	first := awaitDial()
	lifecycle(core.LifecycleInactive)
	lifecycle(core.LifecycleActive)
	if first.ctx.Err() != nil {
		t.Fatal("a foreground interruption cancelled the connection")
	}
	lifecycle(core.LifecycleBackground)
	select {
	case <-first.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("backgrounding left the old connection running")
	}
	lifecycle(core.LifecycleInactive)
	lifecycle(core.LifecycleActive)
	awaitDial()
	if m.Draft != "keep this draft" {
		t.Fatal("foreground reconnect lost the draft")
	}
}

func settleNative(t *testing.T, app *nsgotest.App, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		app.Settle()
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("native work did not finish; texts %q", app.Texts())
}

func nativeWorker(t *testing.T, app *nsgotest.App, work func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); work() }()
	settleNative(t, app, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
}

func startNativePhone(t *testing.T, os core.OS, secrets map[[2]string]string) (*nsgotest.App, *App) {
	t.Helper()
	if nsgotest.Target() != "" {
		t.Skip("headless native service model")
	}
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	m := NewMobileApp()
	m.DataDir = t.TempDir()
	m.DialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	app := nsgotest.Start(t, m.Render, nsgotest.Size(390, 844), nsgotest.Environment(core.Environment{OS: os, Idiom: core.IdiomPhone, Locale: "en", ScanCode: true, Network: &core.Network{Online: true}}), func(a *nsgotest.App) {
		a.Secrets = &core.MemorySecrets{}
		for key, value := range secrets {
			_ = a.Secrets.WriteSecret(core.SecretItem{Service: key[0], Account: key[1]}, value)
		}
	})
	t.Cleanup(func() { m.Close(); app.Settle() })
	settleNative(t, app, func() bool { return m.Loaded })
	return app, m
}

func awaitRegistry(t *testing.T, app *nsgotest.App, count int) phoneclient.MobileDesktops {
	t.Helper()
	var saved phoneclient.MobileDesktops
	settleNative(t, app, func() bool {
		raw, ok := testSecrets(app)[[2]string{"Miya", phonecache.DesktopsKey}]
		if !ok {
			return false
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		_, tombstone := testSecrets(app)[[2]string{"Miya", "nsgo-desktop-link"}]
		_, old := testSecrets(app)[[2]string{"Miya", "desktop-link"}]
		return err == nil && json.Unmarshal(data, &saved) == nil && len(saved.Items) == count && tombstone && !old
	})
	return saved
}

func TestNativeQRPairingPersistsMultipleDesktops(t *testing.T) {
	for _, os := range []core.OS{core.OSIOS, core.OSAndroid} {
		t.Run(string(os), func(t *testing.T) {
			app, m := startNativePhone(t, os, nil)
			first, second := nativeLink(), nativeLink()
			app.ScanCode = func(nsgotest.CodeScan) (*core.ScannedCode, error) {
				return &core.ScannedCode{Text: " \n" + first + "\n"}, nil
			}
			app.Press("id:pair-scan")
			settleNative(t, app, func() bool { return m.Link == first })
			if len(app.Scans) != 1 || m.scanning {
				t.Fatal("scan was not completed")
			}
			awaitRegistry(t, app, 1)
			changePhone(app, m, func() { m.pages.Open("pair") })
			app.Type("id:pair-code", second)
			app.Press("id:pair-connect")
			app.Settle()
			if m.Link != second || m.pages.Top() != "" {
				t.Fatal("manual fallback did not pair")
			}
			saved := awaitRegistry(t, app, 2)
			if saved.Active != m.CurrentDesktopID() {
				t.Fatal("active desktop was not saved")
			}
			changePhone(app, m, func() { m.RenameDesktop(saved.Active, "Office Mac") })
			settleNative(t, app, func() bool {
				raw, _ := base64.StdEncoding.DecodeString(testSecrets(app)[[2]string{"Miya", phonecache.DesktopsKey}])
				return strings.Contains(string(raw), "Office Mac")
			})
			restored, next := startNativePhone(t, os, testSecrets(app))
			settleNative(t, restored, func() bool { return next.Link == second })
			if len(next.Desktops.Items) != 2 || next.DesktopName() != "Office Mac" || next.PersistenceError != "" {
				t.Fatal("secure registry did not survive restart", next.Desktops, next.PersistenceError)
			}
			if testSecrets(app)[[2]string{"Miya", "tunnel-secret"}] != testSecrets(restored)[[2]string{"Miya", "tunnel-secret"}] {
				t.Fatal("tunnel identity changed")
			}
			changePhone(restored, next, func() { next.RemoveDesktop(next.CurrentDesktopID()) })
			awaitRegistry(t, restored, 1)
			changePhone(restored, next, func() { next.Forget() })
			awaitRegistry(t, restored, 0)
			empty, forgotten := startNativePhone(t, os, testSecrets(restored))
			empty.Settle()
			if forgotten.Link != "" || len(forgotten.Desktops.Items) != 0 {
				t.Fatal("Forget resurrected a desktop")
			}
			// Preferences may contain public UI settings, never the connection code.
			for _, key := range []string{"desktop-link", "nsgo-desktop-link", phonecache.DesktopsKey, "tunnel-secret"} {
				if v, _ := app.Storage().GetItem(key); v != "" {
					t.Fatalf("credential in preferences: %s", key)
				}
			}
		})
	}
}

func TestNativeQRRejectsInvalidCancelAndCameraFailure(t *testing.T) {
	app, m := startNativePhone(t, core.OSIOS, nil)
	for _, result := range []struct {
		code      string
		ok        bool
		err       error
		wantError bool
	}{
		{"", false, nil, false}, {"", true, nil, true}, {"https://example.com", true, nil, true}, {"", false, errors.New("camera denied"), true},
	} {
		changePhone(app, m, func() { m.Error = "" })
		app.ScanCode = func(nsgotest.CodeScan) (*core.ScannedCode, error) {
			if !result.ok {
				return nil, result.err
			}
			return &core.ScannedCode{Text: result.code}, result.err
		}
		app.Press("id:pair-scan")
		app.Settle()
		if m.Link != "" || m.scanning || (m.Error != "") != result.wantError {
			t.Fatalf("scan result: link %q, error %q", m.Link, m.Error)
		}
	}
	// Invalid Add desktop input must keep the sheet and existing desktop.
	first := nativeLink()
	changePhone(app, m, func() { m.pair(first); m.pages.Open("pair"); m.pairCode.Set("invalid") })
	app.Press("id:pair-connect")
	app.Settle()
	if m.Link != first || m.pages.Top() != "pair" || m.pairCode.Get() != "invalid" {
		t.Fatal("invalid code dismissed pairing or replaced desktop")
	}
	// A late native result cannot replace a desktop selected during the scan.
	app.ScanCode = func(nsgotest.CodeScan) (*core.ScannedCode, error) {
		m.Connect(first)
		return &core.ScannedCode{Text: nativeLink()}, nil
	}
	app.Press("id:pair-scan")
	app.Settle()
	if m.Link != first {
		t.Fatal("stale scan replaced desktop")
	}
}

func TestNativeSecretsMigration(t *testing.T) {
	link := nativeLink()
	encoded := base64.StdEncoding.EncodeToString
	for _, tc := range []struct {
		name                string
		service, key, value string
	}{
		{"NSGo singleton", "Miya", "nsgo-desktop-link", encoded([]byte(link))},
		{"core singleton", "Miya", "desktop-link", encoded([]byte(link))},
		{"MyGo iOS singleton", "com.daodao.miya", "desktop-link", link},
		{"MyGo iOS registry", "com.daodao.miya", phonecache.DesktopsKey, `{"version":1,"items":[{"link":` + strconvQuote(link) + `,"name":"Old Mac","renamed":true}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed := map[[2]string]string{{tc.service, tc.key}: tc.value, {"Miya", "tunnel-secret"}: encoded(bytes.Repeat([]byte{7}, 32))}
			if tc.service != "Miya" {
				delete(seed, [2]string{"Miya", "tunnel-secret"})
				seed[[2]string{tc.service, "tunnel-secret"}] = string(bytes.Repeat([]byte{7}, 32))
			}
			app, m := startNativePhone(t, core.OSIOS, seed)
			settleNative(t, app, func() bool { return m.Link == link })
			awaitRegistry(t, app, 1)
			if m.PersistenceError != "" {
				t.Fatal(m.PersistenceError)
			}
			if tc.service != "Miya" {
				settleNative(t, app, func() bool { _, exists := testSecrets(app)[[2]string{tc.service, tc.key}]; return !exists })
			}
			if testSecrets(app)[[2]string{"Miya", "tunnel-secret"}] != encoded(bytes.Repeat([]byte{7}, 32)) {
				t.Fatal("migration replaced tunnel identity")
			}
		})
	}
	t.Run("empty tombstone", func(t *testing.T) {
		app, m := startNativePhone(t, core.OSIOS, map[[2]string]string{{"Miya", "nsgo-desktop-link"}: "", {"Miya", "desktop-link"}: encoded([]byte(link))})
		awaitRegistry(t, app, 0)
		if m.Link != "" {
			t.Fatal("tombstone resurrected singleton")
		}
	})
}

func strconvQuote(value string) string { data, _ := json.Marshal(value); return string(data) }

func TestNativeSecretRoundtripAndFailures(t *testing.T) {
	for _, os := range []core.OS{core.OSIOS, core.OSAndroid} {
		t.Run(string(os), func(t *testing.T) {
			app, m := startNativePhone(t, os, nil)
			nativeWorker(t, app, func() {
				for _, value := range [][]byte{nil, []byte("\x00\xff秘密"), []byte("replacement")} {
					if err := m.Store.Set("test", value); err != nil {
						t.Error(err)
					}
					got, err := m.Store.Get("test")
					if err != nil || !bytes.Equal(got, value) {
						t.Errorf("secret roundtrip: %x %v", got, err)
					}
				}
				if err := m.Store.Delete("test"); err != nil {
					t.Error(err)
				}
				if _, err := m.Store.Get("test"); !errors.Is(err, errSecretNotFound) {
					t.Error("missing secret identity", err)
				}
				if err := m.Store.Delete("test"); err != nil {
					t.Error("idempotent delete", err)
				}
			})
		})
	}
}

func TestNativeNetworkChangesAndPreparation(t *testing.T) {
	app, m := startNativePhone(t, core.OSIOS, nil)
	changePhone(app, m, func() { m.Cancel = func() {}; m.Wake = make(chan struct{}, 1) })
	setNetwork := func(online, expensive bool) {
		app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Network: &core.Network{Online: online, Expensive: expensive}})
		app.Settle()
	}
	assertWake := func(want bool) {
		t.Helper()
		select {
		case <-m.Wake:
			if !want {
				t.Fatal("unchanged/offline path retried")
			}
		default:
			if want {
				t.Fatal("new path did not wake reconnect")
			}
		}
	}
	setNetwork(true, false)
	assertWake(false)
	setNetwork(true, true)
	assertWake(true)
	setNetwork(false, true)
	assertWake(false)
	setNetwork(true, true)
	assertWake(true)
	changePhone(app, m, func() { m.Connected = true })
	generation := m.Generation
	setNetwork(true, false)
	assertWake(false)
	if !m.Connected || m.Generation != generation {
		t.Fatal("path change tore down a live connection")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	nativeWorker(t, app, func() {
		if err := m.PrepareNetwork(ctx, "https://relay.example"); !errors.Is(err, context.Canceled) {
			t.Error("cancellation ignored", err)
		}
	})
	n := &phoneNetwork{addresses: []net.Addr{&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(128, 128)}}}
	interfaces, err := n.interfaces()
	if err != nil || len(interfaces) != 1 || interfaces[0].Flags&net.FlagUp == 0 || len(interfaces[0].AltAddrs) != 1 {
		t.Fatalf("Android route inventory: %+v %v", interfaces, err)
	}
	interfaces[0].AltAddrs[0].(*net.IPNet).IP[0] = 0
	if n.addresses[0].(*net.IPNet).IP[0] != 0x20 {
		t.Fatal("worker mutated retained address")
	}
}

func testSecretItem(key [2]string) core.SecretItem {
	return core.SecretItem{Service: key[0], Account: key[1]}
}

func testSecrets(app *nsgotest.App) map[[2]string]string {
	out := map[[2]string]string{}
	for item, value := range app.Secrets.Items() {
		out[[2]string{item.Service, item.Account}] = value
	}
	return out
}

type failingRegistryStore struct {
	phonecache.SecretStore
	fail atomic.Bool
}

func (s *failingRegistryStore) Set(key string, value []byte) error {
	if key == phonecache.DesktopsKey && s.fail.Load() {
		return errors.New("locked registry")
	}
	return s.SecretStore.Set(key, value)
}

func TestNativeMigrationRetainsSourceUntilRegistryWriteSucceeds(t *testing.T) {
	app, m := startNativePhone(t, core.OSIOS, nil)
	awaitRegistry(t, app, 0)
	link := nativeLink()
	_ = app.Secrets.DeleteSecret(testSecretItem([2]string{"Miya", phonecache.DesktopsKey}))
	_ = app.Secrets.WriteSecret(testSecretItem([2]string{"Miya", "nsgo-desktop-link"}), base64.StdEncoding.EncodeToString([]byte(link)))
	store := &failingRegistryStore{SecretStore: m.Store}
	store.fail.Store(true)
	changePhone(app, m, func() { m.Store = store; m.Loaded = false })
	nativeWorker(t, app, func() { m.LoadSaved(errSecretNotFound) })
	settleNative(t, app, func() bool { return m.PersistenceError != "" })
	if _, ok := testSecrets(app)[[2]string{"Miya", phonecache.DesktopsKey}]; ok {
		t.Fatal("failed registry write replaced destination")
	}
	if testSecrets(app)[[2]string{"Miya", "nsgo-desktop-link"}] != base64.StdEncoding.EncodeToString([]byte(link)) {
		t.Fatal("failed migration retired singleton")
	}
	store.fail.Store(false)
	changePhone(app, m, m.ReloadDesktops)
	awaitRegistry(t, app, 1)
}

func TestNativeUnreadableRegistryCannotBeOverwrittenByPairing(t *testing.T) {
	original := base64.StdEncoding.EncodeToString([]byte("invalid registry"))
	app, m := startNativePhone(t, core.OSIOS, map[[2]string]string{{"Miya", phonecache.DesktopsKey}: original})
	if m.PersistenceError == "" {
		t.Fatal("unreadable registry was ignored")
	}
	changePhone(app, m, func() { m.pair(nativeLink()) })
	// Flush a later native operation so any erroneous asynchronous write can run.
	nativeWorker(t, app, func() { _, _ = m.Store.Get("tunnel-secret") })
	if testSecrets(app)[[2]string{"Miya", phonecache.DesktopsKey}] != original {
		t.Fatal("pairing overwrote unreadable saved desktops")
	}
	link := nativeLink()
	repaired := `{"version":1,"items":[{"link":` + strconvQuote(link) + `,"name":"Recovered"}]}`
	_ = app.Secrets.WriteSecret(testSecretItem([2]string{"Miya", phonecache.DesktopsKey}), base64.StdEncoding.EncodeToString([]byte(repaired)))
	changePhone(app, m, m.ReloadDesktops)
	awaitRegistry(t, app, 2)
}
