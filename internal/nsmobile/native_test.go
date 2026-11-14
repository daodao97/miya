package nsmobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
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
	app := nsgotest.Start(t, m.Render, nsgotest.Size(390, 844), nsgotest.Environment(core.Environment{OS: os, Idiom: core.IdiomPhone, Locale: "en"}), func(a *nsgotest.App) {
		a.Secrets = maps.Clone(secrets)
		a.NetworkAvailable = true
		a.NetworkInterfaces = []core.NetworkInterface{{Name: "wlan0", Kind: core.NetworkWiFi}}
	})
	t.Cleanup(func() { m.Close(); app.Settle() })
	settleNative(t, app, func() bool { return m.Loaded })
	return app, m
}

func awaitRegistry(t *testing.T, app *nsgotest.App, count int) phoneclient.MobileDesktops {
	t.Helper()
	var saved phoneclient.MobileDesktops
	settleNative(t, app, func() bool {
		raw, ok := app.Secrets[[2]string{"Miya", phonecache.DesktopsKey}]
		if !ok {
			return false
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		_, tombstone := app.Secrets[[2]string{"Miya", "nsgo-desktop-link"}]
		_, old := app.Secrets[[2]string{"Miya", "desktop-link"}]
		return err == nil && json.Unmarshal(data, &saved) == nil && len(saved.Items) == count && tombstone && !old
	})
	return saved
}

func TestNativeQRPairingPersistsMultipleDesktops(t *testing.T) {
	for _, os := range []core.OS{core.OSIOS, core.OSAndroid} {
		t.Run(string(os), func(t *testing.T) {
			app, m := startNativePhone(t, os, nil)
			first, second := nativeLink(), nativeLink()
			app.ScanQRCode = func() (string, bool, error) { return " \n" + first + "\n", true, nil }
			app.Press("id:pair-scan")
			settleNative(t, app, func() bool { return m.Link == first })
			if app.QRScans != 1 || m.scanning {
				t.Fatal("scan was not completed")
			}
			awaitRegistry(t, app, 1)
			changePhone(app, m, func() { m.sheet.Set("pair") })
			app.Type("id:pair-code", second)
			app.Press("id:pair-connect")
			app.Settle()
			if m.Link != second || m.sheet.Get() != "" {
				t.Fatal("manual fallback did not pair")
			}
			saved := awaitRegistry(t, app, 2)
			if saved.Active != m.CurrentDesktopID() {
				t.Fatal("active desktop was not saved")
			}
			changePhone(app, m, func() { m.RenameDesktop(saved.Active, "Office Mac") })
			settleNative(t, app, func() bool {
				raw, _ := base64.StdEncoding.DecodeString(app.Secrets[[2]string{"Miya", phonecache.DesktopsKey}])
				return strings.Contains(string(raw), "Office Mac")
			})
			restored, next := startNativePhone(t, os, app.Secrets)
			settleNative(t, restored, func() bool { return next.Link == second })
			if len(next.Desktops.Items) != 2 || next.DesktopName() != "Office Mac" || next.PersistenceError != "" {
				t.Fatal("secure registry did not survive restart", next.Desktops, next.PersistenceError)
			}
			if app.Secrets[[2]string{"Miya", "tunnel-secret"}] != restored.Secrets[[2]string{"Miya", "tunnel-secret"}] {
				t.Fatal("tunnel identity changed")
			}
			changePhone(restored, next, func() { next.RemoveDesktop(next.CurrentDesktopID()) })
			awaitRegistry(t, restored, 1)
			changePhone(restored, next, func() { next.Forget() })
			awaitRegistry(t, restored, 0)
			empty, forgotten := startNativePhone(t, os, restored.Secrets)
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
		app.ScanQRCode = func() (string, bool, error) { return result.code, result.ok, result.err }
		app.Press("id:pair-scan")
		app.Settle()
		if m.Link != "" || m.scanning || (m.Error != "") != result.wantError {
			t.Fatalf("scan result: link %q, error %q", m.Link, m.Error)
		}
	}
	// Invalid Add desktop input must keep the sheet and existing desktop.
	first := nativeLink()
	changePhone(app, m, func() { m.pair(first); m.sheet.Set("pair"); m.pairCode.Set("invalid") })
	app.Press("id:pair-connect")
	app.Settle()
	if m.Link != first || m.sheet.Get() != "pair" || m.pairCode.Get() != "invalid" {
		t.Fatal("invalid code dismissed pairing or replaced desktop")
	}
	// A late native result cannot replace a desktop selected during the scan.
	app.ScanQRCode = func() (string, bool, error) { m.Connect(first); return nativeLink(), true, nil }
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
				settleNative(t, app, func() bool { _, exists := app.Secrets[[2]string{tc.service, tc.key}]; return !exists })
			}
			if app.Secrets[[2]string{"Miya", "tunnel-secret"}] != encoded(bytes.Repeat([]byte{7}, 32)) {
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
				if _, err := m.Store.Get("test"); !errors.Is(err, core.ErrSecretNotFound) {
					t.Error("missing secret identity", err)
				}
				if err := m.Store.Delete("test"); err != nil {
					t.Error("idempotent delete", err)
				}
			})
			failure := errors.New("locked")
			app.PlatformServiceError = failure
			nativeWorker(t, app, func() {
				if _, err := m.Store.Get("test"); !errors.Is(err, failure) {
					t.Error("read failure hidden", err)
				}
				if err := m.Store.Set("test", []byte("secret")); !errors.Is(err, failure) {
					t.Error("write failure hidden", err)
				}
			})
			if _, exists := app.Secrets[[2]string{"Miya", "test"}]; exists {
				t.Fatal("failed write changed store")
			}
			app.PlatformServiceError = nil
		})
	}
}

func TestNativeNetworkChangesAndPreparation(t *testing.T) {
	for _, os := range []core.OS{core.OSIOS, core.OSAndroid} {
		t.Run(string(os), func(t *testing.T) {
			app, m := startNativePhone(t, os, nil)
			changePhone(app, m, func() { m.Cancel = func() {}; m.Wake = make(chan struct{}, 1) })
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
			app.SetNetworkPath(true)
			assertWake(false)
			app.NetworkInterfaces = []core.NetworkInterface{{Name: "rmnet0", Kind: core.NetworkCellular}}
			app.SetNetworkPath(true)
			assertWake(true)
			app.SetNetworkPath(false)
			assertWake(false)
			app.SetNetworkPath(true)
			assertWake(true)
			// Unknown Android names still carry transport identity; ordering does not.
			app.NetworkInterfaces = []core.NetworkInterface{{Kind: core.NetworkWiFi}, {Kind: core.NetworkVPN}}
			app.SetNetworkPath(true)
			assertWake(true)
			slices.Reverse(app.NetworkInterfaces)
			app.SetNetworkPath(true)
			assertWake(false)

			n := &phoneNetwork{cx: m.cx}
			var warmed bool
			n.warmup = func(ctx context.Context, _ string) error { warmed = true; return ctx.Err() }
			nativeWorker(t, app, func() {
				if err := n.prepare(context.Background(), "https://relay.example"); err != nil {
					t.Error(err)
				}
			})
			if !warmed {
				t.Fatal("available path skipped system preparation")
			}
			warmed = false
			app.SetNetworkPath(false)
			nativeWorker(t, app, func() {
				if err := n.prepare(context.Background(), "https://relay.example"); err == nil {
					t.Error("offline path allowed dial")
				}
			})
			if warmed {
				t.Fatal("offline path started warmup")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			nativeWorker(t, app, func() {
				if err := n.prepare(ctx, "https://relay.example"); !errors.Is(err, context.Canceled) {
					t.Error("cancellation ignored", err)
				}
			})
			app.PlatformServiceError = errors.New("path failed")
			nativeWorker(t, app, func() {
				if err := n.prepare(context.Background(), "https://relay.example"); err == nil {
					t.Error("native path failure hidden")
				}
			})
			app.PlatformServiceError = nil
			n.update(core.NetworkPath{Available: true, Interfaces: []core.NetworkInterface{{Name: "wlan0", Kind: core.NetworkWiFi}, {Name: "wlan0", Kind: core.NetworkVPN}, {Kind: core.NetworkCellular}}})
			interfaces, err := n.interfaces()
			if err != nil || len(interfaces) != 1 || interfaces[0].Name != "wlan0" || interfaces[0].Flags&net.FlagUp == 0 || interfaces[0].AltAddrs == nil {
				t.Fatalf("Android path inventory: %+v %v", interfaces, err)
			}
			n.addresses = []net.Addr{&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(128, 128)}}
			withIP, _ := n.interfaces()
			withIP[0].AltAddrs[0].(*net.IPNet).IP[0] = 0
			if n.addresses[0].(*net.IPNet).IP[0] != 0x20 {
				t.Fatal("worker mutated retained address")
			}
			interfaces[0].Name = "changed"
			again, _ := n.interfaces()
			if again[0].Name != "wlan0" {
				t.Fatal("worker mutated retained path")
			}
			changePhone(app, m, func() { m.Connected = true })
			generation := m.Generation
			app.NetworkInterfaces = []core.NetworkInterface{{Name: "live-path", Kind: core.NetworkEthernet}}
			app.SetNetworkPath(true)
			assertWake(false)
			if !m.Connected || m.Generation != generation {
				t.Fatal("path change tore down a live connection")
			}
			changePhone(app, m, func() { m.EnterBackground() })
			app.SetNetworkPath(true)
			if m.Cancel != nil {
				t.Fatal("background path reconnected")
			}
		})
	}
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
	delete(app.Secrets, [2]string{"Miya", phonecache.DesktopsKey})
	app.Secrets[[2]string{"Miya", "nsgo-desktop-link"}] = base64.StdEncoding.EncodeToString([]byte(link))
	store := &failingRegistryStore{SecretStore: m.Store}
	store.fail.Store(true)
	changePhone(app, m, func() { m.Store = store; m.Loaded = false })
	nativeWorker(t, app, func() { m.LoadSaved(core.ErrSecretNotFound) })
	settleNative(t, app, func() bool { return m.PersistenceError != "" })
	if _, ok := app.Secrets[[2]string{"Miya", phonecache.DesktopsKey}]; ok {
		t.Fatal("failed registry write replaced destination")
	}
	if app.Secrets[[2]string{"Miya", "nsgo-desktop-link"}] != base64.StdEncoding.EncodeToString([]byte(link)) {
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
	if app.Secrets[[2]string{"Miya", phonecache.DesktopsKey}] != original {
		t.Fatal("pairing overwrote unreadable saved desktops")
	}
	link := nativeLink()
	repaired := `{"version":1,"items":[{"link":` + strconvQuote(link) + `,"name":"Recovered"}]}`
	app.Secrets[[2]string{"Miya", phonecache.DesktopsKey}] = base64.StdEncoding.EncodeToString([]byte(repaired))
	changePhone(app, m, m.ReloadDesktops)
	awaitRegistry(t, app, 2)
}
