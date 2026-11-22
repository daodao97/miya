package statecard

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testCard(session, purpose string) Card {
	return Card{Backend: "codex", Session: session, Purpose: purpose, Author: AuthorAgent, WrittenAtMs: 1000}
}

// cardFiles counts what the store left in dir.
func cardFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return len(entries)
}

// A card is a few short lines: whitespace is folded, empty items are dropped
// and anything longer is refused rather than cut.
func TestNormalizeKeepsACardShort(t *testing.T) {
	got, err := Normalize(Card{
		Purpose:    "  Ship \n the importer ",
		Done:       []string{" parser ", "", "  "},
		Remaining:  []string{"wire\tthe CLI"},
		WaitingFor: " which  format? ",
		Checked:    []string{"go test ./..."},
		References: []string{" abc123 "},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Card{
		Purpose:    "Ship the importer",
		Done:       []string{"parser"},
		Remaining:  []string{"wire the CLI"},
		WaitingFor: "which format?",
		Checked:    []string{"go test ./..."},
		References: []string{"abc123"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized card = %+v, want %+v", got, want)
	}

	long := strings.Repeat("\u00e9", MaxText+1)
	many := make([]string, MaxItems+1)
	for i := range many {
		many[i] = "item"
	}
	for name, c := range map[string]Card{
		"no purpose":     {Purpose: "  "},
		"long purpose":   {Purpose: long},
		"long question":  {Purpose: "p", WaitingFor: long},
		"long item":      {Purpose: "p", Remaining: []string{long}},
		"too many items": {Purpose: "p", Done: many},
		"long reference": {Purpose: "p", References: []string{strings.Repeat("r", MaxReference+1)}},
	} {
		if _, err := Normalize(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The limit counts characters, not bytes.
	if _, err := Normalize(Card{Purpose: strings.Repeat("\u00e9", MaxText)}); err != nil {
		t.Errorf("a purpose of %d two-byte characters: %v", MaxText, err)
	}
}

// A conversation has one card, a later one replaces it, and the cards are
// there again after a restart.
func TestStoreKeepsOneCardPerConversation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := testCard("s1", "Ship the importer")
	first.Remaining = []string{"wire the CLI"}
	first.WaitingFor = "Which format?"
	if err := s.Put(first); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(testCard("s2", "Fix the flaky test")); err != nil {
		t.Fatal(err)
	}
	second := testCard("s1", "Ship the importer")
	second.Done = []string{"wire the CLI"}
	second.WaitingFor = "Release now?"
	second.WrittenAtMs = 2000
	if err := s.Put(second); err != nil {
		t.Fatal(err)
	}
	if n := cardFiles(t, dir); n != 2 {
		t.Fatalf("%d files for 2 conversations", n)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get(Key("codex", "s1"))
	if !ok || !reflect.DeepEqual(got, second) || reopened.Len() != 2 {
		t.Fatalf("after reopening: %+v, %v, %d cards; want %+v", got, ok, reopened.Len(), second)
	}
	if !got.Waiting() {
		t.Error("a current card with a question does not wait")
	}
	// The same session of another agent is another conversation.
	if _, ok := reopened.Get(Key("claude", "s1")); ok {
		t.Error("another agent's session has the card")
	}

	// An outdated card stays outdated across a restart and waits for nobody.
	if changed, err := reopened.SetStale(second.Key(), true); err != nil || !changed {
		t.Fatalf("marking the card outdated: %v, %v", changed, err)
	}
	if changed, err := reopened.SetStale(second.Key(), true); err != nil || changed {
		t.Fatalf("marking it outdated again: %v, %v", changed, err)
	}
	if changed, err := reopened.SetStale(Key("codex", "unknown"), true); err != nil || changed {
		t.Fatalf("marking a card that does not exist: %v, %v", changed, err)
	}
	third, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := third.Get(second.Key()); !got.Stale || got.Waiting() {
		t.Fatalf("after reopening the outdated card: %+v", got)
	}
}

// A card follows its conversation to another session and goes when the
// conversation does.
func TestStoreMovesAndDeletesCards(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := testCard("old", "Ship the importer")
	if err := s.Put(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(c.Key(), "codex", "new"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(c.Key()); ok {
		t.Error("the old session kept the card")
	}
	moved, ok := s.Get(Key("codex", "new"))
	if !ok || moved.Session != "new" || moved.Purpose != c.Purpose || cardFiles(t, dir) != 1 {
		t.Fatalf("moved card = %+v, %v, %d files", moved, ok, cardFiles(t, dir))
	}
	// Nothing to move is not an error.
	for _, from := range []string{"", c.Key(), moved.Key()} {
		if err := s.Move(from, "codex", "new"); err != nil {
			t.Errorf("move from %q: %v", from, err)
		}
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Get(moved.Key()); !ok || reopened.Len() != 1 {
		t.Fatalf("after reopening: %d cards", reopened.Len())
	}

	if err := s.Delete(moved.Key()); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(moved.Key()); err != nil {
		t.Errorf("deleting a card that is gone: %v", err)
	}
	if _, ok := s.Get(moved.Key()); ok || cardFiles(t, dir) != 0 {
		t.Fatalf("the card is still there: %d files", cardFiles(t, dir))
	}
}

// Without the background service there are no cards and none is written;
// a conversation without a session cannot have one.
func TestStoreWithoutAServiceOrASession(t *testing.T) {
	var none *Store
	if _, ok := none.Get(Key("codex", "s1")); ok || none.Len() != 0 {
		t.Error("a missing store has a card")
	}
	if err := none.Put(testCard("s1", "p")); err != ErrNoStore {
		t.Errorf("writing without a store: %v", err)
	}
	if changed, err := none.SetStale(Key("codex", "s1"), true); changed || err != nil {
		t.Errorf("marking without a store: %v, %v", changed, err)
	}
	if err := none.Move(Key("codex", "s1"), "codex", "s2"); err != nil {
		t.Errorf("moving without a store: %v", err)
	}
	if err := none.Delete(Key("codex", "s1")); err != nil {
		t.Errorf("deleting without a store: %v", err)
	}

	if Key("codex", "") != "" || Key("", "s1") != "" {
		t.Error("a conversation without a session or an agent has a key")
	}
	s, err := Open(filepath.Join(t.TempDir(), "status"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(testCard("", "p")); err == nil {
		t.Error("a card without a session was saved")
	}
}

// One broken file does not take the other cards with it.
func TestOpenSkipsFilesThatAreNotCards(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	good := testCard("s1", "Ship the importer")
	if err := s.Put(good); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"broken.json":   "{not json",
		"stranger.json": `{"backend":"codex","session":"s9","purpose":"under another name"}`,
		"empty.json":    `{}`,
		"notes.txt":     "not a card",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.Get(good.Key()); !ok || got.Purpose != good.Purpose || reopened.Len() != 1 {
		t.Fatalf("after reopening beside broken files: %+v, %v, %d cards", got, ok, reopened.Len())
	}
}

// A reader is told who wrote the card, that Miya did not verify it and
// whether the conversation went on since.
func TestLineAndText(t *testing.T) {
	c := testCard("s1", "Ship the importer")
	if got := Line(c); got != "Ship the importer · Nothing remaining" {
		t.Errorf("line = %q", got)
	}
	text := Text(c)
	for _, want := range []string{"written by the agent", "Miya has not verified it.", "Purpose: Ship the importer", "Remaining: nothing"} {
		if !strings.Contains(text, want) {
			t.Errorf("text misses %q:\n%s", want, text)
		}
	}
	for _, absent := range []string{"Outdated", "Done:", "Waiting for the person", "Checked", "References:"} {
		if strings.Contains(text, absent) {
			t.Errorf("text of a bare card has %q:\n%s", absent, text)
		}
	}

	c.Done = []string{"parser"}
	c.Remaining = []string{"a", "b", "c", "d"}
	c.WaitingFor = "Which format?"
	c.Checked = []string{"go test ./..."}
	c.References = []string{"abc123"}
	c.Stale = true
	if got, want := Line(c), "Ship the importer · Remaining: a; b; c (+1 more) · Waiting for the person: Which format? · Outdated"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	text = Text(c)
	for _, want := range []string{
		"Outdated: the conversation went on after it was written.",
		"Done:\n- parser",
		"Remaining:\n- a\n- b\n- c\n- d",
		"Waiting for the person: Which format?",
		"Checked, by the writer's own account:\n- go test ./...",
		"References:\n- abc123",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text misses %q:\n%s", want, text)
		}
	}
	if strings.HasSuffix(text, "\n") {
		t.Error("text ends with a line break")
	}

	c.Author = "miya"
	if !strings.Contains(Text(c), "written by Miya") {
		t.Errorf("a card Miya filled in is not labelled:\n%s", Text(c))
	}
}
