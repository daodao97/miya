package memory

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func listed(t *testing.T, s *Store, scope Scope, id string) bool {
	t.Helper()
	entries, _, _, err := s.List(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == id {
			return true
		}
	}
	return false
}

func tombstones(t *testing.T, s *Store, scope Scope, id string) int {
	t.Helper()
	d, err := s.load(scope)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range d.Tombstones {
		if m.ID == id {
			n++
		}
	}
	return n
}

func TestDeleteActiveEntryForgetsThenRemovesRecord(t *testing.T) {
	s, scope := fixture(t)
	m := mutation(s, "SECRET-DELETE")
	m.Sources = []Source{{Thread: "miya://source/thread/codex/session", MessageID: "u1", EvidenceKind: "user_statement", ObservedAt: s.now(), Quote: "SECRET-QUOTE"}}
	e := save(t, s, scope, m)
	if err := s.Delete(scope, e.ID, e.Version); err != nil {
		t.Fatal(err)
	}
	if listed(t, s, scope, e.ID) {
		t.Fatal("deleted entry still listed")
	}
	if b, _ := os.ReadFile(s.path(scope)); strings.Contains(string(b), "SECRET") {
		t.Fatalf("deleted body or quote on disk: %s", b)
	}
	reopened := New(s.root)
	if tombstones(t, reopened, scope, e.ID) != 1 {
		t.Fatal("deleting an active entry must keep its tombstone")
	}
	if got, err := reopened.Search(scope, "SECRET", "", 5); err != nil || len(got) != 0 {
		t.Fatalf("deleted memory recalled: %v %v", got, err)
	}
	m.ID, m.Version = e.ID, e.Version
	if _, err := reopened.Save(scope, m); err == nil {
		t.Fatal("stale update revived a deleted entry")
	}
}

func TestDeleteForgottenEntryKeepsSingleTombstone(t *testing.T) {
	s, scope := fixture(t)
	e := save(t, s, scope, mutation(s, "已忘记的旧约定"))
	forgotten, err := s.Forget(scope, e.ID, e.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(scope, forgotten.ID, e.Version); err == nil {
		t.Fatal("delete accepted a stale version")
	} else if !errors.As(err, new(*ConflictError)) {
		t.Fatalf("stale delete error = %v", err)
	}
	if err := s.Delete(scope, forgotten.ID, forgotten.Version); err != nil {
		t.Fatal(err)
	}
	if listed(t, s, scope, e.ID) {
		t.Fatal("forgotten record still listed after delete")
	}
	if n := tombstones(t, s, scope, e.ID); n != 1 {
		t.Fatalf("tombstones = %d, want 1", n)
	}
	if err := s.Delete(scope, e.ID, forgotten.Version); err == nil {
		t.Fatal("deleting a missing entry succeeded")
	}
}

func TestDeleteInvalidatesPreviouslyInjectedEntry(t *testing.T) {
	s, scope, _ := extractionFixture(t)
	e := save(t, s, scope, mutation(s, "手机构建使用 scripts/build-ios.sh"))
	first, err := s.Prepare(scope, "codex-session", "1", "手机构建", "")
	if err != nil || !strings.Contains(first.Block, e.Content) {
		t.Fatalf("entry not injected: %+v %v", first, err)
	}
	_ = s.FinishPrepared(scope, first, true)
	if err := s.Delete(scope, e.ID, e.Version); err != nil {
		t.Fatal(err)
	}
	if s.ValidatePrepared(scope, first) {
		t.Fatal("retry reused a deleted entry")
	}
	next, err := s.Prepare(scope, "codex-session", "1", "其他问题", "")
	if err != nil || strings.Contains(next.Block, e.Content) || !strings.Contains(next.Block, "unavailable") || !strings.Contains(next.Block, e.ID) {
		t.Fatalf("deleted entry not invalidated once: %q %v", next.Block, err)
	}
	_ = s.FinishPrepared(scope, next, true)
	if again, _ := s.Prepare(scope, "codex-session", "1", "其他问题", ""); strings.Contains(again.Block, e.ID) {
		t.Fatal("invalidation repeated")
	}
}
