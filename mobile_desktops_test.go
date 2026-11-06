package main

import (
	bytes "bytes"
	context "context"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	mobile "miya/internal/mobile"
	remote "miya/internal/remote"
	os "os"
	filepath "path/filepath"
	sync "sync"
	testing "testing"
	time "time"

	mygo "github.com/egoist/mygo"
	ui "github.com/egoist/mygo/ui"
	tailcat "github.com/tailscale/tailcat"
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
		if key == appmodel.MobileDesktopsKey {
			s.attempts <- errors.New("Keychain unavailable")
		}
		return errors.New("Keychain unavailable")
	}
	s.data[key] = bytes.Clone(data)
	if key == appmodel.MobileDesktopsKey {
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

func multiDesktopApp(t *testing.T) *mobile.App {
	t.Helper()
	m := mobile.NewMobileApp()
	m.Loaded = true
	m.DataDir = t.TempDir()
	m.Router().Transition = ui.TransitionNone
	m.DialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { m.Disconnect(); m.FlushCache(true) })
	return m
}

func waitDesktopSave(t *testing.T, m *mobile.App, store *desktopTestStore) {
	t.Helper()
	want, _ := json.Marshal(m.Desktops)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case data := <-store.writes:
			if bytes.Equal(data, want) {
				m.DesktopWrites.Mu.Lock()
				m.DesktopWrites.Mu.Unlock()
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
	saved, migrated, err := mobile.ReadMobileDesktops(store)
	if err != nil || !migrated || len(saved.Items) != 1 || saved.Active != saved.Items[0].ID || saved.Items[0].Link != link {
		t.Fatal("legacy pairing did not migrate without changing its cache key")
	}
	addr, _ := remote.ParseLink(link)
	_, err = saved.Remember(string(addr), "Mac Studio")
	if err != nil || len(saved.Items) != 1 || saved.Items[0].Name != "Mac Studio" || saved.Items[0].Link != link {
		t.Fatal("the same desktop's URL and bare address created duplicate pairings")
	}
	m := multiDesktopApp(t)
	m.Store, m.Desktops = store, saved
	m.SaveDesktops()
	waitDesktopSave(t, m, store)
	if _, err := store.Get("desktop-link"); !errors.Is(err, mygo.ErrSecretNotFound) {
		t.Fatal("legacy singleton was not removed after its replacement was saved")
	}
	restored, migrated, err := mobile.ReadMobileDesktops(store)
	if err != nil || migrated || len(restored.Items) != 1 || restored.Active != saved.Active {
		t.Fatal("saved desktops did not survive a relaunch")
	}
}

func TestMobileDesktopsDoNotOverwriteInvalidOrFailedStorage(t *testing.T) {
	store := newDesktopTestStore()
	link := testDesktopLink()
	_ = store.Set("desktop-link", []byte(link))
	_ = store.Set(appmodel.MobileDesktopsKey, []byte(`{"version":2}`))
	if _, _, err := mobile.ReadMobileDesktops(store); err == nil {
		t.Fatal("unsupported registry silently fell back to a singleton")
	}
	store.mu.Lock()
	delete(store.data, appmodel.MobileDesktopsKey)
	store.fail = true
	store.mu.Unlock()
	for len(store.attempts) > 0 {
		<-store.attempts
	}
	m := multiDesktopApp(t)
	m.Store = store
	m.Desktops, _, _ = mobile.ReadMobileDesktops(store)
	m.SaveDesktops()
	select {
	case err := <-store.attempts:
		if err == nil {
			t.Fatal("expected a failed migration write")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration write did not run")
	}
	m.DesktopWrites.Mu.Lock()
	m.DesktopWrites.Mu.Unlock()
	if _, err := store.Get("desktop-link"); err != nil {
		t.Fatal("failed migration deleted the original pairing")
	}
	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	m.SaveDesktops()
	waitDesktopSave(t, m, store)
	m.Desktops = mobile.MobileDesktops{Version: 1}
	m.SaveDesktops()
	waitDesktopSave(t, m, store)
	restored, _, err := mobile.ReadMobileDesktops(store)
	if err != nil || len(restored.Items) != 0 {
		t.Fatal("an empty saved registry resurrected a removed desktop")
	}
}

func TestMobileDesktopSwitchKeepsCachesAndOptionsSeparate(t *testing.T) {
	m := multiDesktopApp(t)
	a, b := testDesktopLink(), testDesktopLink()
	m.Connect(a)
	aID, oldGeneration := m.CurrentDesktopID(), m.Generation
	m.Host = "Mac A"
	m.State.Projects = []appmodel.PhoneProject{{Path: "/a", Name: "A", Threads: []appmodel.Thread{{ID: 7, ServerID: "shared", Title: "Only A"}}}}
	m.Remember("shared", []appmodel.Message{{ID: 1, Content: "Only A"}})
	m.Options = map[string]remote.SendOptions{appmodel.AgentCodex: {Model: "model-a"}}
	m.SaveOptions()
	aCache := m.CacheDir
	m.BadgeRevision = 999
	m.Commands = map[string][]appmodel.ComposerChoice{"/a": {{Name: "secret-command"}}}
	m.ApprovalFields = map[string]map[string]string{"1": {"answer": "A"}}
	m.Viewer.SetThreads([]appmodel.Thread{{ID: 7, ServerID: "shared", Messages: []appmodel.Message{{Content: "Only A"}}}})
	m.Viewer.SetActive(7)
	m.Connect(b)
	if len(m.Desktops.Items) != 2 || len(m.State.Projects) != 0 || len(m.CachedMessages("shared")) != 0 || len(m.Options) != 0 || len(m.Commands) != 0 || len(m.ApprovalFields) != 0 || m.Viewer.Current().ID != 0 || m.BadgeRevision != 0 {
		t.Fatal("switching desktops leaked the previous desktop's state")
	}
	m.Host = "Mac B"
	m.State.Projects = []appmodel.PhoneProject{{Path: "/b", Name: "B", Threads: []appmodel.Thread{{ID: 7, ServerID: "shared", Title: "Only B"}}}}
	m.Remember("shared", []appmodel.Message{{ID: 1, Content: "Only B"}})
	m.Options = map[string]remote.SendOptions{appmodel.AgentCodex: {Model: "model-b"}}
	m.SaveOptions()
	bID, bCache := m.CurrentDesktopID(), m.CacheDir
	m.SwitchDesktop(aID)
	if m.Host != "Mac A" || len(m.State.Projects) != 1 || m.State.Projects[0].Path != "/a" || m.CachedMessages("shared")[0].Content != "Only A" || m.Options[appmodel.AgentCodex].Model != "model-a" {
		t.Fatal("switching back did not restore the desktop's own cache and options")
	}
	if m.Generation <= oldGeneration {
		t.Fatal("old connection generation remained active")
	}
	for _, path := range []string{aCache, bCache} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("switching deleted a paired desktop's cache")
		}
	}
	m.SwitchDesktop(bID)
	if m.CachedMessages("shared")[0].Content != "Only B" || m.Options[appmodel.AgentCodex].Model != "model-b" {
		t.Fatal("desktop B's cache or options were overwritten")
	}
}

func TestMobileDesktopRemovalOnlyRemovesSelectedPairing(t *testing.T) {
	m := multiDesktopApp(t)
	a, b, c := testDesktopLink(), testDesktopLink(), testDesktopLink()
	for _, link := range []string{a, b, c} {
		m.Connect(link)
		m.State.Projects = []appmodel.PhoneProject{{Name: "cached"}}
	}
	aID, _ := appmodel.DesktopID(a)
	bID, _ := appmodel.DesktopID(b)
	cID, generation := m.CurrentDesktopID(), m.Generation
	bCache := filepath.Join(m.DataDir, "threads", appmodel.CacheName(b))
	m.RemoveDesktop(bID)
	if len(m.Desktops.Items) != 2 || m.CurrentDesktopID() != cID || m.Generation != generation {
		t.Fatal("removing an inactive desktop disturbed the current connection")
	}
	if _, err := os.Stat(bCache); !os.IsNotExist(err) {
		t.Fatal("removed desktop's cache was retained")
	}
	if m.PushDisabled {
		t.Fatal("removing a desktop disabled notifications for all desktops")
	}
	m.RemoveDesktop(cID)
	if len(m.Desktops.Items) != 1 || m.CurrentDesktopID() != aID {
		t.Fatal("removing the active desktop did not select the remaining desktop")
	}
	m.RemoveDesktop(aID)
	if m.Link != "" || len(m.Desktops.Items) != 0 || m.Router().Path() != "/" {
		t.Fatal("removing the last desktop did not return to pairing")
	}
}

func TestMobileDesktopManagerNavigationAndSwitch(t *testing.T) {
	m := multiDesktopApp(t)
	a, b := testDesktopLink(), testDesktopLink()
	m.Connect(a)
	_, _ = m.Desktops.Remember(a, "Mac Studio")
	m.Host = "Mac Studio"
	other, _ := m.Desktops.Remember(b, "MacBook Pro")
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("Switch desktop"); err != nil {
		t.Fatal(err)
	}
	if m.Router().Path() != "/desktops" || !tt.HasText("Mac Studio") || !tt.HasText("MacBook Pro") || tt.HasText(a) || tt.HasText(b) {
		t.Fatal("desktop manager did not show saved desktops without exposing pairing secrets")
	}
	saveMobileNativeSnapshot(t, tt, "desktop-manager")
	if err := tt.Click("Add desktop"); err != nil {
		t.Fatal(err)
	}
	if m.Router().Path() != "/desktop/add" || !tt.HasText("Scan to connect") || m.Link != a {
		t.Fatal("adding a desktop disconnected the current desktop")
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if m.Router().Path() != "/desktops" {
		t.Fatal("adding a desktop did not return to its manager")
	}
	if err := tt.Click("Connect to desktop MacBook Pro"); err != nil {
		t.Fatal(err)
	}
	if m.CurrentDesktopID() != other.ID || len(m.Desktops.Items) != 2 || m.Router().Path() != "/" {
		t.Fatal("selecting a saved desktop did not switch without removing other pairings")
	}
}

func TestMobileDesktopSwitchPreservesDraftAndAttachments(t *testing.T) {
	m := multiDesktopApp(t)
	a, b := testDesktopLink(), testDesktopLink()
	m.Connect(a)
	m.State.Projects = []appmodel.PhoneProject{{Path: "/a", Threads: []appmodel.Thread{{ID: 1, ServerID: "a-thread", Title: "A"}}}}
	m.OpenThread(m.State.Projects[0].Threads[0])
	m.Draft = "A 的草稿"
	photo := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(photo, []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Images = []string{photo}
	aID := m.CurrentDesktopID()
	m.Connect(b)
	if m.Draft != "" || len(m.Images) != 0 {
		t.Fatal("desktop A's composer leaked into desktop B")
	}
	if _, err := os.Stat(photo); err != nil {
		t.Fatal("switching desktops deleted an unsent attachment")
	}
	m.Draft = "B 的草稿"
	bID := m.CurrentDesktopID()
	m.SwitchDesktop(aID)
	if m.Open != 1 || m.Viewer.Current().ServerID != "a-thread" || m.Draft != "A 的草稿" || len(m.Images) != 1 || m.Images[0] != photo {
		t.Fatal("switching back did not restore the conversation, draft and attachment")
	}
	m.SwitchDesktop(bID)
	if m.Draft != "B 的草稿" {
		t.Fatal("desktop B's draft was overwritten")
	}
}

func TestMobileDesktopPersistenceKeepsLatestSelection(t *testing.T) {
	m := multiDesktopApp(t)
	store := newDesktopTestStore()
	m.Store = store
	a, b := testDesktopLink(), testDesktopLink()
	m.Connect(a)
	m.Connect(b)
	for i := 0; i < 12; i++ {
		_, _ = m.Desktops.Remember(b, fmt.Sprintf("Mac %d", i))
		m.SaveDesktops()
	}
	waitDesktopSave(t, m, store)
	saved, _, err := mobile.ReadMobileDesktops(store)
	if err != nil || saved.Active != m.CurrentDesktopID() || len(saved.Items) != 2 || saved.Items[1].Name != "Mac 11" {
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
	appmodel.MigrateDesktopOptions(dir, link)
	destination := filepath.Join(dir, "threads", appmodel.CacheName(link), "message-options.json")
	if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, data) {
		t.Fatal("migration lost the current desktop's message options")
	}
	if err := os.WriteFile(destination, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	appmodel.MigrateDesktopOptions(dir, link)
	if got, _ := os.ReadFile(destination); string(got) != "{}" {
		t.Fatal("migration overwrote desktop-specific options")
	}
}

func TestMobileNotificationsSelectTheCorrectDesktop(t *testing.T) {
	m := multiDesktopApp(t)
	a, b := testDesktopLink(), testDesktopLink()
	m.Connect(a)
	aID := m.CurrentDesktopID()
	m.State.Projects = []appmodel.PhoneProject{{Path: "/a", Threads: []appmodel.Thread{{ID: 7, ServerID: "only-a", Title: "A"}}}}
	m.Connect(b)
	m.State.Projects = []appmodel.PhoneProject{{Path: "/b", Threads: []appmodel.Thread{{ID: 7, ServerID: "only-b", Title: "B"}}}}
	m.OpenNotification(map[string]string{"thread": "7", "serverId": "only-a", "project": "/a", "agent": appmodel.AgentCodex})
	if m.CurrentDesktopID() != aID || m.Viewer.Current().ServerID != "only-a" {
		t.Fatal("notification opened a same-numbered conversation on the wrong desktop")
	}
	m.OpenNotification(map[string]string{"thread": "7"})
	if m.Router().Path() != "/desktops" || m.DesktopNotice == "" {
		t.Fatal("an ambiguous legacy notification should ask the user to choose a desktop")
	}
}

func TestMobileDesktopManagerSharesDrawerBackBehavior(t *testing.T) {
	m := multiDesktopApp(t)
	m.Connect(testDesktopLink())
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("Switch desktop"); err != nil {
		t.Fatal(err)
	}
	openMobileDrawer(t, tt)
	if m.Router().Path() != "/desktops" || !m.Menu || m.Router().InteractiveBack {
		t.Fatal("desktop manager should return to the drawer")
	}
}
