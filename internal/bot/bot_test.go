package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid(name string) Bot {
	return Bot{Name: name, Agent: "claude-code", Project: "/work/app"}
}

func TestNormalize(t *testing.T) {
	b := Bot{Name: "  Release \n helper ", Agent: " codex ", Project: "/work/app", Access: "edit"}
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	if b.Name != "Release helper" || b.Agent != "codex" || b.ID == "" {
		t.Fatalf("normalized to %+v", b)
	}
	for name, bad := range map[string]Bot{
		"no name":    {Agent: "codex", Project: "/work/app"},
		"no agent":   {Name: "A", Project: "/work/app"},
		"no project": {Name: "A", Agent: "codex", Project: "app"},
		"access":     {Name: "A", Agent: "codex", Project: "/work/app", Access: "admin"},
		"long name":  {Name: strings.Repeat("a", maxName+1), Agent: "codex", Project: "/work/app"},
	} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("%s: accepted %+v", name, bad)
		}
	}
}

func TestPutKeepsTheStandingConversation(t *testing.T) {
	var s Store
	b, err := s.Put(valid("Ops"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.SetSession(b.ID, "session-1") || s.SetSession(b.ID, "session-1") {
		t.Fatal("SetSession should report a change once")
	}
	s.SetPreview(b.ID, "done")

	// An edit from a window cannot name the session.
	b.Name, b.Note, b.Session = "Ops lead", "Watch the deploys", "forged"
	got, err := s.Put(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != "session-1" || got.Preview != "done" || got.Name != "Ops lead" || len(s.Bots) != 1 {
		t.Fatalf("edit gave %+v in %d Bots", got, len(s.Bots))
	}

	// Another agent cannot continue the old conversation.
	b.Agent = "codex"
	if got, err = s.Put(b); err != nil {
		t.Fatal(err)
	}
	if got.Session != "" || got.Preview != "" {
		t.Fatalf("changing the agent kept the conversation: %+v", got)
	}
}

func TestPutRefusesASecondBotOfTheSameName(t *testing.T) {
	var s Store
	if _, err := s.Put(valid("Ops")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(valid("ops")); err == nil {
		t.Fatal("a second Bot named ops was accepted")
	}
	if len(s.Bots) != 1 {
		t.Fatalf("%d Bots", len(s.Bots))
	}
}

func TestResetAndDelete(t *testing.T) {
	var s Store
	b, _ := s.Put(valid("Ops"))
	s.SetSession(b.ID, "session-1")
	s.SetPreview(b.ID, "done")
	if !s.Reset(b.ID) || s.Bots[0].Session != "" || s.Bots[0].Preview != "" {
		t.Fatalf("reset left %+v", s.Bots[0])
	}
	if s.Reset("missing") || s.Delete("missing") {
		t.Fatal("an unknown Bot was changed")
	}
	if !s.Delete(b.ID) || len(s.Bots) != 0 {
		t.Fatalf("delete left %d Bots", len(s.Bots))
	}
}

func TestSaveLeavesOutTheRunningThread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bots.json")
	var s Store
	b, _ := s.Put(valid("Ops"))
	s.SetSession(b.ID, "session-1")
	s.Bots[0].Thread = 7
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if s.Bots[0].Thread != 7 {
		t.Fatal("Save changed the store")
	}
	got := Load(path)
	if len(got.Bots) != 1 || got.Bots[0].Thread != 0 || got.Bots[0].Session != "session-1" || got.Bots[0].Name != "Ops" {
		t.Fatalf("loaded %+v", got.Bots)
	}
}

func TestLoadSkipsWhatItCannotUse(t *testing.T) {
	dir := t.TempDir()
	if got := Load(filepath.Join(dir, "missing.json")); len(got.Bots) != 0 {
		t.Fatalf("a missing file gave %+v", got.Bots)
	}
	path := filepath.Join(dir, "bots.json")
	data := `{"bots":[{"id":"a1","name":"Ops","agent":"codex","project":"/work/app"},{"id":"b2","agent":"codex","project":"/work/app"}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path); len(got.Bots) != 1 || got.Bots[0].ID != "a1" {
		t.Fatalf("loaded %+v", got.Bots)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path); len(got.Bots) != 0 {
		t.Fatalf("a damaged file gave %+v", got.Bots)
	}
}

func TestMatchFindsABotByItsSession(t *testing.T) {
	var s Store
	b, _ := s.Put(valid("Ops"))
	if _, ok := s.Match("claude-code", "/work/app", ""); ok {
		t.Fatal("a Bot with no session matched")
	}
	s.SetSession(b.ID, "session-1")
	if i, ok := s.Match("claude-code", "/work/app", "session-1"); !ok || s.Bots[i].ID != b.ID {
		t.Fatal("the Bot's session did not match")
	}
	if _, ok := s.Match("codex", "/work/app", "session-1"); ok {
		t.Fatal("another agent's session matched")
	}
	if _, ok := s.Match("claude-code", "/work/other", "session-1"); ok {
		t.Fatal("another project's session matched")
	}
}

func TestIntroAndPreview(t *testing.T) {
	text := Intro(Bot{Name: "Ops", Note: "Watch the deploys"})
	if !strings.HasPrefix(text, "\n\n") || !strings.Contains(text, `"Ops"`) || !strings.HasSuffix(text, "Watch the deploys") {
		t.Fatalf("intro %q", text)
	}
	if text := Intro(Bot{Name: "Ops"}); strings.Contains(text, "role") {
		t.Fatalf("intro without a note mentions a role: %q", text)
	}
	if got := PreviewOf("  first\n\nsecond  "); got != "first second" {
		t.Fatalf("preview %q", got)
	}
	long := PreviewOf(strings.Repeat("字", 300))
	if n := len([]rune(long)); n != previewRunes+1 || !strings.HasSuffix(long, "…") {
		t.Fatalf("long preview has %d runes", n)
	}
}
