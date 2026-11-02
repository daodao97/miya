package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, Scope) {
	t.Helper()
	scope, err := ProjectScope(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(t.TempDir(), "memory"))
	s.now = func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }
	return s, scope
}
func mutation(s *Store, content string) Mutation {
	return Mutation{Kind: "fact", Content: content, ObservedAt: s.now(), Sources: []Source{{EvidenceKind: "manual", ObservedAt: s.now()}}}
}
func save(t *testing.T, s *Store, scope Scope, m Mutation) Entry {
	t.Helper()
	e, err := s.Save(scope, m)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestScopeCanonicalizationAndIsolation(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "one", "repo")
	other := filepath.Join(root, "two", "repo")
	for _, p := range []string{real, other} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	a, _ := ProjectScope(real)
	alias, _ := ProjectScope(link)
	b, _ := ProjectScope(other)
	if a != alias || a.ProjectKey == b.ProjectKey {
		t.Fatal("scope aliasing or collision")
	}
	if _, err := ProjectScope("repo"); err == nil {
		t.Fatal("relative scope accepted")
	}
	s, _ := fixture(t)
	save(t, s, a, mutation(s, "后台升级等空闲"))
	got, err := s.Search(b, "后台升级", "", 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("cross-project leak: %v %v", got, err)
	}
}
func TestVersionedCorrectionsPersistAndRejectStaleEdits(t *testing.T) {
	s, scope := fixture(t)
	old := save(t, s, scope, mutation(s, "先更新协议"))
	m := mutation(s, "暂缓更新协议")
	m.ID = old.ID
	m.Version = old.Version
	next := save(t, s, scope, m)
	if next.ID != old.ID || next.Version != 2 || next.Supersedes != 1 {
		t.Fatalf("correction: %+v", next)
	}
	if _, err := s.Save(scope, m); err == nil {
		t.Fatal("stale edit accepted")
	} else {
		var conflict *ConflictError
		if !errors.As(err, &conflict) || conflict.Current.Version != 2 {
			t.Fatalf("missing current version: %v", err)
		}
	}
	reopened := New(s.root)
	entries, _, rev, err := reopened.List(scope)
	if err != nil || rev != 2 || len(entries) != 1 || entries[0].Content != next.Content {
		t.Fatalf("reopen: %v %v %d", entries, err, rev)
	}
	for _, p := range []string{s.root, filepath.Join(s.root, "projects"), s.path(scope)} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o700)
		if !info.IsDir() {
			want = 0o600
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s permissions %o", p, info.Mode().Perm())
		}
	}
}
func TestPersistenceFailureCannotPublishOrDestroyPreviousRevision(t *testing.T) {
	s, scope := fixture(t)
	old := save(t, s, scope, mutation(s, "已保存事实"))
	before, _ := os.ReadFile(s.path(scope))
	s.replace = func(string, []byte) error { return errors.New("disk full") }
	m := mutation(s, "不能保存的新事实")
	m.ID = old.ID
	m.Version = old.Version
	if _, err := s.Save(scope, m); err == nil {
		t.Fatal("save reported success")
	}
	if _, err := s.Forget(scope, old.ID, old.Version); err == nil {
		t.Fatal("forget reported success")
	}
	if err := s.Configure(scope, Settings{Disabled: true}); err == nil {
		t.Fatal("settings reported success")
	}
	entries, settings, rev, err := s.List(scope)
	if err != nil || rev != 1 || entries[0].Content != old.Content || settings.Disabled {
		t.Fatal("uncommitted change leaked")
	}
	after, _ := os.ReadFile(s.path(scope))
	if string(before) != string(after) {
		t.Fatal("previous revision destroyed")
	}
}
func TestCorruptUnknownAndMismatchedScopesStayUntouched(t *testing.T) {
	for _, which := range []string{"broken", "format", "scope", "entry"} {
		t.Run(which, func(t *testing.T) {
			s, scope := fixture(t)
			e := save(t, s, scope, mutation(s, "已保存事实"))
			b, _ := os.ReadFile(s.path(scope))
			var d document
			_ = json.Unmarshal(b, &d)
			switch which {
			case "broken":
				b = []byte("{not json")
			case "format":
				d.Format++
				b, _ = json.Marshal(d)
			case "scope":
				d.Scope = UserScope()
				b, _ = json.Marshal(d)
			case "entry":
				d.Entries[0].Scope = UserScope()
				b, _ = json.Marshal(d)
			}
			if err := os.WriteFile(s.path(scope), b, 0o600); err != nil {
				t.Fatal(err)
			}
			reopened := New(s.root)
			if _, _, _, err := reopened.List(scope); err == nil {
				t.Fatal("bad scope accepted")
			}
			if _, err := reopened.Save(scope, mutation(s, "new")); err == nil {
				t.Fatal("bad file overwritten")
			}
			if _, err := reopened.Forget(scope, e.ID, e.Version); err == nil {
				t.Fatal("bad file forgotten")
			}
			after, _ := os.ReadFile(s.path(scope))
			if string(after) != string(b) {
				t.Fatal("original changed")
			}
		})
	}
}
func TestForgetRemovesAllVersionsAndQuotesAndSurvivesRestart(t *testing.T) {
	s, scope := fixture(t)
	m := mutation(s, "SECRET-OLD")
	m.Sources = []Source{{Thread: "miya://source/thread/codex/session", MessageID: "u1", EvidenceKind: "user_statement", ObservedAt: s.now(), Quote: "SECRET-QUOTE"}}
	old := save(t, s, scope, m)
	m.Content = "SECRET-NEW"
	m.ID = old.ID
	m.Version = old.Version
	next := save(t, s, scope, m)
	forgotten, err := s.Forget(scope, next.ID, next.Version)
	if err != nil {
		t.Fatal(err)
	}
	if forgotten.State != "forgotten" || forgotten.Version != 3 {
		t.Fatalf("bad tombstone: %+v", forgotten)
	}
	b, _ := os.ReadFile(s.path(scope))
	if strings.Contains(string(b), "SECRET") {
		t.Fatalf("forgotten body or quote on disk: %s", b)
	}
	reopened := New(s.root)
	got, err := reopened.Search(scope, "SECRET", "", 5)
	if err != nil || len(got) != 0 {
		t.Fatal("forgotten memory recalled")
	}
	m.Version = 2
	if _, err := reopened.Save(scope, m); err == nil {
		t.Fatal("stale task revived memory")
	}
	m.ID = ""
	m.Version = 0
	e := save(t, reopened, scope, m)
	if e.ID == old.ID {
		t.Fatal("explicit re-remember reused tombstone ID")
	}
}
func TestConcurrentUpdatesHaveExactlyOneWinner(t *testing.T) {
	s, scope := fixture(t)
	e := save(t, s, scope, mutation(s, "初始状态"))
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := range 10 {
		wg.Go(func() {
			m := mutation(s, fmt.Sprintf("状态 %d", i))
			m.ID = e.ID
			m.Version = e.Version
			_, err := s.Save(scope, m)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d winners", success)
	}
}
func TestSearchChineseEnglishTaskExpirationAndPreferences(t *testing.T) {
	s, scope := fixture(t)
	save(t, s, scope, mutation(s, "在 connectService 中检查后台升级"))
	save(t, s, scope, mutation(s, "手机协议先不修改"))
	user := mutation(s, "中文解释后台升级")
	user.Kind = "preference"
	save(t, s, UserScope(), user)
	m := mutation(s, "后台升级正在验证")
	m.Kind = "progress"
	m.TaskKey = "issue-123"
	progress := save(t, s, scope, m)
	if !progress.ValidUntil.Equal(s.now().Add(7 * 24 * time.Hour)) {
		t.Fatal("progress TTL")
	}
	for _, query := range []string{"CONNECTSERVICE", "后台升级"} {
		got, err := s.Search(scope, query, "", 5)
		if err != nil || len(got) != 1 {
			t.Fatalf("query %q: %v %v", query, got, err)
		}
	}
	got, err := s.Search(scope, "后台升级", "issue-123", 5)
	if err != nil || len(got) != 2 || got[0].ID != progress.ID {
		t.Fatalf("task rank: %v %v", got, err)
	}
	if err := s.Configure(scope, Settings{UseUserPreferences: true}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Search(scope, "后台升级", "", 5)
	if err != nil || len(got) != 2 {
		t.Fatalf("enabled preferences: %v %v", got, err)
	}
	for _, query := range []string{"", "xyzNotRelated"} {
		got, _ = s.Search(scope, query, "", 5)
		if len(got) != 0 {
			t.Fatal("unclear query recalled data")
		}
	}
	s.now = func() time.Time { return progress.ValidUntil }
	got, _ = s.Search(scope, "后台升级", "issue-123", 5)
	for _, e := range got {
		if e.ID == progress.ID {
			t.Fatal("expired progress recalled")
		}
	}
	if err := s.Configure(scope, Settings{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Search(scope, "后台升级", "", 5)
	if len(got) != 0 {
		t.Fatal("disabled project recalled memory")
	}
	entries, _, _, _ := s.List(scope)
	if len(entries) != 3 {
		t.Fatal("disable deleted data")
	}
}
func TestSearchBoundAndNoSliceAliasing(t *testing.T) {
	s, scope := fixture(t)
	for range 10 {
		save(t, s, scope, mutation(s, "memory "+strings.Repeat("正文", 450)))
	}
	got, err := s.Search(scope, "memory", "", 100)
	if err != nil || len(got) == 0 || len(got) > 4 {
		t.Fatalf("budget: %d %v", len(got), err)
	}
	got[0].Sources[0].Quote = "modified by reader"
	again, _, _, _ := s.List(scope)
	for _, e := range again {
		if e.Sources[0].Quote != "" {
			t.Fatal("reader mutated store")
		}
	}
}
func TestPromotionChecksVersionBeforeTouchingProjectFile(t *testing.T) {
	s, scope := fixture(t)
	old := save(t, s, scope, mutation(s, "长期约定"))
	m := mutation(s, "修改约定")
	m.ID = old.ID
	m.Version = old.Version
	save(t, s, scope, m)
	touched := false
	if _, err := s.PromoteWith(scope, old.ID, old.Version, func() error { touched = true; return nil }); err == nil || touched {
		t.Fatal("stale promotion touched file")
	}
	if _, err := s.PromoteWith(scope, old.ID, 2, func() error { return errors.New("read only") }); err == nil {
		t.Fatal("failed write promoted memory")
	}
	got, _ := s.Search(scope, "约定", "", 5)
	if len(got) != 1 {
		t.Fatal("failed promotion hid memory")
	}
	e, err := s.Promote(scope, old.ID, 2)
	if err != nil || e.State != "promoted" {
		t.Fatal("promotion failed")
	}
	got, _ = s.Search(scope, "约定", "", 5)
	if len(got) != 0 {
		t.Fatal("promoted memory recalled")
	}
}

func TestPreferencePromotionAndPartialCommit(t *testing.T) {
	s, scope := fixture(t)
	m := mutation(s, "偏好中文解释")
	m.Kind = "preference"
	old := save(t, s, scope, m)
	user, err := s.PromotePreference(scope, old.ID, old.Version)
	if err != nil {
		t.Fatal(err)
	}
	if user.Scope != UserScope() || user.ID == old.ID {
		t.Fatal("promotion reused scope/identity")
	}
	entries, _, _, _ := s.List(scope)
	if entries[0].State != "promoted" {
		t.Fatal("original not promoted")
	}
	if _, err = s.PromotePreference(scope, old.ID, old.Version); err == nil {
		t.Fatal("stale promotion duplicated preference")
	}
	// Cross-file promotion is explicitly two commits. Report the surviving ID.
	old = save(t, s, scope, m)
	real := s.replace
	s.replace = func(path string, b []byte) error {
		if path == s.path(scope) {
			return errors.New("disk full")
		}
		return real(path, b)
	}
	user, err = s.PromotePreference(scope, old.ID, old.Version)
	if err == nil || user.ID == "" || !strings.Contains(err.Error(), user.ID) {
		t.Fatalf("partial commit obscured: %+v %v", user, err)
	}
}

func TestSearchUsesCompactSourcesWithoutRemovingStoredEvidence(t *testing.T) {
	s, scope := fixture(t)
	m := mutation(s, "手机协议暂缓升级")
	m.Sources = nil
	for i := range 8 {
		m.Sources = append(m.Sources, Source{Thread: "miya://source/thread/codex/session", MessageID: fmt.Sprintf("user-%d", i), EvidenceKind: "user_statement", ObservedAt: s.now(), Quote: strings.Repeat("原文", 450)})
	}
	e := save(t, s, scope, m)
	got, err := s.Search(scope, "手机协议", "", 5)
	if err != nil || len(got) != 1 || got[0].ID != e.ID {
		t.Fatalf("source history exhausted recall budget: %v %v", got, err)
	}
	for _, src := range got[0].Sources {
		if src.Quote != "" {
			t.Fatal("recall copied full evidence")
		}
	}
	entries, _, _, _ := s.List(scope)
	if entries[0].Sources[0].Quote == "" {
		t.Fatal("compact recall removed stored evidence")
	}
}
