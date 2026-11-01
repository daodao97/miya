package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
	"miya/internal/remote"
)

type desktopTestStore struct {
	mu       sync.Mutex
	data     map[string][]byte
	writes   chan []byte
	attempts chan error
	fail     bool
}

func newDesktopTestStore() *desktopTestStore {
	return &desktopTestStore{data: map[string][]byte{}, writes: make(chan []byte, 64), attempts: make(chan error, 64)}
}
func (s *desktopTestStore) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, ok := s.data[key]; ok {
		return bytes.Clone(data), nil
	}
	return nil, mygo.ErrSecretNotFound
}
func (s *desktopTestStore) Set(key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		if key == mobileDesktopsKey {
			s.attempts <- errors.New("Keychain unavailable")
		}
		return errors.New("Keychain unavailable")
	}
	s.data[key] = bytes.Clone(data)
	if key == mobileDesktopsKey {
		s.attempts <- nil
		s.writes <- bytes.Clone(data)
	}
	return nil
}
func (s *desktopTestStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func testDesktopLink() string {
	identity := tailcat.NewPrivateKey()
	identity.Public.RegionID = 1
	return remote.Link(identity.Public.Addr())
}

func multiDesktopApp(t *testing.T) *mobileApp {
	t.Helper()
	m := newMobileApp()
	m.loaded = true
	m.dataDir = t.TempDir()
	m.router().Transition = ui.TransitionNone
	m.dialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { m.disconnect(); m.flushCache(true) })
	return m
}

func waitDesktopSave(t *testing.T, m *mobileApp, store *desktopTestStore) {
	t.Helper()
	want, _ := json.Marshal(m.desktops)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case data := <-store.writes:
			if bytes.Equal(data, want) {
				m.desktopWrites.mu.Lock()
				m.desktopWrites.mu.Unlock()
				return
			}
		case <-deadline:
			t.Fatal("desktop registry was not persisted")
		}
	}
}

func TestMobileDesktopsMigrateLegacyAndDeduplicateLinks(t *testing.T) {
	store := newDesktopTestStore()
	link := testDesktopLink()
	_ = store.Set("desktop-link", []byte(link))
	saved, migrated, err := readMobileDesktops(store)
	if err != nil || !migrated || len(saved.Items) != 1 || saved.Active != saved.Items[0].ID || saved.Items[0].Link != link {
		t.Fatal("legacy pairing did not migrate without changing its cache key")
	}
	addr, _ := remote.ParseLink(link)
	_, err = saved.remember(string(addr), "Mac Studio")
	if err != nil || len(saved.Items) != 1 || saved.Items[0].Name != "Mac Studio" || saved.Items[0].Link != link {
		t.Fatal("the same desktop's URL and bare address created duplicate pairings")
	}
	m := multiDesktopApp(t)
	m.store, m.desktops = store, saved
	m.saveDesktops()
	waitDesktopSave(t, m, store)
	if _, err := store.Get("desktop-link"); !errors.Is(err, mygo.ErrSecretNotFound) {
		t.Fatal("legacy singleton was not removed after its replacement was saved")
	}
	restored, migrated, err := readMobileDesktops(store)
	if err != nil || migrated || len(restored.Items) != 1 || restored.Active != saved.Active {
		t.Fatal("saved desktops did not survive a relaunch")
	}
}

func TestMobileDesktopsDoNotOverwriteInvalidOrFailedStorage(t *testing.T) {
	store := newDesktopTestStore()
	link := testDesktopLink()
	_ = store.Set("desktop-link", []byte(link))
	_ = store.Set(mobileDesktopsKey, []byte(`{"version":2}`))
	if _, _, err := readMobileDesktops(store); err == nil {
		t.Fatal("unsupported registry silently fell back to a singleton")
	}
	store.mu.Lock()
	delete(store.data, mobileDesktopsKey)
	store.fail = true
	store.mu.Unlock()
	for len(store.attempts) > 0 {
		<-store.attempts
	}
	m := multiDesktopApp(t)
	m.store = store
	m.desktops, _, _ = readMobileDesktops(store)
	m.saveDesktops()
	select {
	case err := <-store.attempts:
		if err == nil {
			t.Fatal("expected a failed migration write")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration write did not run")
	}
	m.desktopWrites.mu.Lock()
	m.desktopWrites.mu.Unlock()
	if _, err := store.Get("desktop-link"); err != nil {
		t.Fatal("failed migration deleted the original pairing")
	}
	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	m.saveDesktops()
	waitDesktopSave(t, m, store)
	m.desktops = mobileDesktops{Version: 1}
	m.saveDesktops()
	waitDesktopSave(t, m, store)
	restored, _, err := readMobileDesktops(store)
	if err != nil || len(restored.Items) != 0 {
		t.Fatal("an empty saved registry resurrected a removed desktop")
	}
}

func TestMobileDesktopSwitchKeepsCachesAndOptionsSeparate(t *testing.T) {
	m := multiDesktopApp(t)
	a, b := testDesktopLink(), testDesktopLink()
	m.connect(a)
	aID, oldGeneration := m.currentDesktopID(), m.generation
	m.host = "Mac A"
	m.state.Projects = []phoneProject{{Path: "/a", Name: "A", Threads: []Thread{{ID: 7, ServerID: "shared", Title: "Only A"}}}}
	m.remember("shared", []Message{{ID: 1, Content: "Only A"}})
	m.options = map[string]remote.SendOptions{agentCodex: {Model: "model-a"}}
	m.saveOptions()
	aCache := m.cacheDir
	m.badgeRevision = 999
	m.commands = map[string][]composerChoice{"/a": {{Name: "secret-command"}}}
	m.approvalFields = map[string]map[string]string{"1": {"answer": "A"}}
	m.view.c.setThreads([]Thread{{ID: 7, ServerID: "shared", Messages: []Message{{Content: "Only A"}}}})
	m.view.c.setActive(7)
	m.connect(b)
	if len(m.desktops.Items) != 2 || len(m.state.Projects) != 0 || len(m.cachedMessages("shared")) != 0 || len(m.options) != 0 || len(m.commands) != 0 || len(m.approvalFields) != 0 || m.view.c.current().ID != 0 || m.badgeRevision != 0 {
		t.Fatal("switching desktops leaked the previous desktop's state")
	}
	m.host = "Mac B"
	m.state.Projects = []phoneProject{{Path: "/b", Name: "B", Threads: []Thread{{ID: 7, ServerID: "shared", Title: "Only B"}}}}
	m.remember("shared", []Message{{ID: 1, Content: "Only B"}})
	m.options = map[string]remote.SendOptions{agentCodex: {Model: "model-b"}}
	m.saveOptions()
	bID, bCache := m.currentDesktopID(), m.cacheDir
	m.switchDesktop(aID)
	if m.host != "Mac A" || len(m.state.Projects) != 1 || m.state.Projects[0].Path != "/a" || m.cachedMessages("shared")[0].Content != "Only A" || m.options[agentCodex].Model != "model-a" {
		t.Fatal("switching back did not restore the desktop's own cache and options")
	}
	if m.generation <= oldGeneration {
		t.Fatal("old connection generation remained active")
	}
	for _, path := range []string{aCache, bCache} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("switching deleted a paired desktop's cache")
		}
	}
	m.switchDesktop(bID)
	if m.cachedMessages("shared")[0].Content != "Only B" || m.options[agentCodex].Model != "model-b" {
		t.Fatal("desktop B's cache or options were overwritten")
	}
}

func TestMobileDesktopRemovalOnlyRemovesSelectedPairing(t *testing.T) {
	m := multiDesktopApp(t)
	a, b, c := testDesktopLink(), testDesktopLink(), testDesktopLink()
	for _, link := range []string{a, b, c} {
		m.connect(link)
		m.state.Projects = []phoneProject{{Name: "cached"}}
	}
	aID, _ := desktopID(a)
	bID, _ := desktopID(b)
	cID, generation := m.currentDesktopID(), m.generation
	bCache := filepath.Join(m.dataDir, "threads", cacheName(b))
	m.removeDesktop(bID)
	if len(m.desktops.Items) != 2 || m.currentDesktopID() != cID || m.generation != generation {
		t.Fatal("removing an inactive desktop disturbed the current connection")
	}
	if _, err := os.Stat(bCache); !os.IsNotExist(err) {
		t.Fatal("removed desktop's cache was retained")
	}
	if m.pushDisabled {
		t.Fatal("removing a desktop disabled notifications for all desktops")
	}
	m.removeDesktop(cID)
	if len(m.desktops.Items) != 1 || m.currentDesktopID() != aID {
		t.Fatal("removing the active desktop did not select the remaining desktop")
	}
	m.removeDesktop(aID)
	if m.link != "" || len(m.desktops.Items) != 0 || m.router().Path() != "/" {
		t.Fatal("removing the last desktop did not return to pairing")
	}
}

func TestMobileDesktopManagerNavigationAndSwitch(t *testing.T) {
	m := multiDesktopApp(t)
	a, b := testDesktopLink(), testDesktopLink()
	m.connect(a)
	_, _ = m.desktops.remember(a, "Mac Studio")
	m.host = "Mac Studio"
	other, _ := m.desktops.remember(b, "MacBook Pro")
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("切换桌面"); err != nil {
		t.Fatal(err)
	}
	if m.router().Path() != "/desktops" || !tt.HasText("Mac Studio") || !tt.HasText("MacBook Pro") || tt.HasText(a) || tt.HasText(b) {
		t.Fatal("desktop manager did not show saved desktops without exposing pairing secrets")
	}
	saveMobileNativeSnapshot(t, tt, "desktop-manager")
	if err := tt.Click("添加桌面"); err != nil {
		t.Fatal(err)
	}
	if m.router().Path() != "/desktop/add" || !tt.HasText("扫码连接") || m.link != a {
		t.Fatal("adding a desktop disconnected the current desktop")
	}
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	if m.router().Path() != "/desktops" {
		t.Fatal("adding a desktop did not return to its manager")
	}
	if err := tt.Click("连接桌面 MacBook Pro"); err != nil {
		t.Fatal(err)
	}
	if m.currentDesktopID() != other.ID || len(m.desktops.Items) != 2 || m.router().Path() != "/" {
		t.Fatal("selecting a saved desktop did not switch without removing other pairings")
	}
}

func TestMobileDesktopSwitchPreservesDraftAndAttachments(t *testing.T) {
	m := multiDesktopApp(t)
	a, b := testDesktopLink(), testDesktopLink()
	m.connect(a)
	m.state.Projects = []phoneProject{{Path: "/a", Threads: []Thread{{ID: 1, ServerID: "a-thread", Title: "A"}}}}
	m.openThread(m.state.Projects[0].Threads[0])
	m.draft = "A 的草稿"
	photo := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(photo, []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	m.images = []string{photo}
	aID := m.currentDesktopID()
	m.connect(b)
	if m.draft != "" || len(m.images) != 0 {
		t.Fatal("desktop A's composer leaked into desktop B")
	}
	if _, err := os.Stat(photo); err != nil {
		t.Fatal("switching desktops deleted an unsent attachment")
	}
	m.draft = "B 的草稿"
	bID := m.currentDesktopID()
	m.switchDesktop(aID)
	if m.open != 1 || m.view.c.current().ServerID != "a-thread" || m.draft != "A 的草稿" || len(m.images) != 1 || m.images[0] != photo {
		t.Fatal("switching back did not restore the conversation, draft and attachment")
	}
	m.switchDesktop(bID)
	if m.draft != "B 的草稿" {
		t.Fatal("desktop B's draft was overwritten")
	}
}

func TestMobileDesktopPersistenceKeepsLatestSelection(t *testing.T) {
	m := multiDesktopApp(t)
	store := newDesktopTestStore()
	m.store = store
	a, b := testDesktopLink(), testDesktopLink()
	m.connect(a)
	m.connect(b)
	for i := 0; i < 12; i++ {
		_, _ = m.desktops.remember(b, fmt.Sprintf("Mac %d", i))
		m.saveDesktops()
	}
	waitDesktopSave(t, m, store)
	saved, _, err := readMobileDesktops(store)
	if err != nil || saved.Active != m.currentDesktopID() || len(saved.Items) != 2 || saved.Items[1].Name != "Mac 11" {
		t.Fatal("an older asynchronous write replaced the latest desktop registry")
	}
}

func TestMobileDesktopMigrationKeepsExistingOptions(t *testing.T) {
	dir := t.TempDir()
	link := testDesktopLink()
	data := []byte(`{"codex":{"model":"legacy-model","effort":"high"}}`)
	if err := os.WriteFile(filepath.Join(dir, "message-options.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	migrateDesktopOptions(dir, link)
	destination := filepath.Join(dir, "threads", cacheName(link), "message-options.json")
	if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, data) {
		t.Fatal("migration lost the current desktop's message options")
	}
	if err := os.WriteFile(destination, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	migrateDesktopOptions(dir, link)
	if got, _ := os.ReadFile(destination); string(got) != "{}" {
		t.Fatal("migration overwrote desktop-specific options")
	}
}

func TestMobileNotificationsSelectTheCorrectDesktop(t *testing.T) {
	m := multiDesktopApp(t)
	a, b := testDesktopLink(), testDesktopLink()
	m.connect(a)
	aID := m.currentDesktopID()
	m.state.Projects = []phoneProject{{Path: "/a", Threads: []Thread{{ID: 7, ServerID: "only-a", Title: "A"}}}}
	m.connect(b)
	m.state.Projects = []phoneProject{{Path: "/b", Threads: []Thread{{ID: 7, ServerID: "only-b", Title: "B"}}}}
	m.openNotification(map[string]string{"thread": "7", "serverId": "only-a", "project": "/a", "agent": agentCodex})
	if m.currentDesktopID() != aID || m.view.c.current().ServerID != "only-a" {
		t.Fatal("notification opened a same-numbered conversation on the wrong desktop")
	}
	m.openNotification(map[string]string{"thread": "7"})
	if m.router().Path() != "/desktops" || m.desktopNotice == "" {
		t.Fatal("an ambiguous legacy notification should ask the user to choose a desktop")
	}
}

func TestMobileDesktopManagerSharesDrawerBackBehavior(t *testing.T) {
	m := multiDesktopApp(t)
	m.connect(testDesktopLink())
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("切换桌面"); err != nil {
		t.Fatal(err)
	}
	openMobileDrawer(t, tt)
	if m.router().Path() != "/desktops" || !m.menu || m.router().InteractiveBack {
		t.Fatal("desktop manager should return to the drawer")
	}
}
