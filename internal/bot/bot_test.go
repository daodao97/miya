package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	// The conversation the person left stays the Bot's.
	if got := s.Bots[0].Others; len(got) != 1 || got[0] != "session-1" {
		t.Fatalf("after reset the other conversations are %v", got)
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

func TestCharterAndPreview(t *testing.T) {
	text := Charter(Bot{Name: "Ops", Note: "Watch the deploys"})
	if !strings.HasPrefix(text, "\n\n") || !strings.Contains(text, `"Ops"`) || !strings.HasSuffix(text, "Watch the deploys") {
		t.Fatalf("charter %q", text)
	}
	if text := Charter(Bot{Name: "Ops"}); strings.Contains(text, "in the person's words") {
		t.Fatalf("charter without a note quotes one: %q", text)
	}
	if got := PreviewOf("  first\n\nsecond  "); got != "first second" {
		t.Fatalf("preview %q", got)
	}
	long := PreviewOf(strings.Repeat("字", 300))
	if n := len([]rune(long)); n != previewRunes+1 || !strings.HasSuffix(long, "…") {
		t.Fatalf("long preview has %d runes", n)
	}
}

// A Bot keeps its other conversations, newest first: the ones the person
// left and the ones delegated tasks started. They follow a session the agent
// moves, and an edit from a window cannot name them.
func TestOtherConversations(t *testing.T) {
	var s Store
	b, _ := s.Put(valid("Ops"))
	if s.AddOther(b.ID, "") || s.AddOther("missing", "task-1") {
		t.Fatal("an empty session or an unknown Bot took a conversation")
	}
	if !s.AddOther(b.ID, "task-1") || s.AddOther(b.ID, "task-1") {
		t.Fatal("AddOther should report a change once")
	}
	s.SetSession(b.ID, "talk-1")
	s.AddOther(b.ID, "task-2")
	if got := s.Bots[0].Others; len(got) != 2 || got[0] != "task-2" || got[1] != "task-1" {
		t.Fatalf("others = %v, want newest first", got)
	}
	if s.AddOther(b.ID, "talk-1") {
		t.Fatal("the person's conversation was added as another")
	}
	for _, session := range []string{"talk-1", "task-1", "task-2"} {
		if i, ok := s.Match("claude-code", "/work/app", session); !ok || s.Bots[i].ID != b.ID {
			t.Fatalf("%s did not match the Bot", session)
		}
	}
	if !s.Rename(b.ID, "task-1", "task-1b") || !s.Rename(b.ID, "talk-1", "talk-1b") || s.Rename(b.ID, "gone", "x") || s.Rename(b.ID, "task-2", "task-2") {
		t.Fatal("Rename reported the wrong changes")
	}
	if s.Bots[0].Session != "talk-1b" || s.Bots[0].Others[1] != "task-1b" {
		t.Fatalf("after the renames %+v", s.Bots[0])
	}

	edit := s.Bots[0]
	edit.Others = []string{"forged"}
	if got, _ := s.Put(edit); len(got.Others) != 2 || got.Others[0] != "task-2" {
		t.Fatalf("an edit set the other conversations to %v", got.Others)
	}
	edit.Agent = "codex"
	if got, _ := s.Put(edit); len(got.Others) != 0 {
		t.Fatalf("another agent kept the other conversations %v", got.Others)
	}

	for i := range maxOthers + 5 {
		s.AddOther(b.ID, strings.Repeat("t", i+1))
	}
	if got := s.Bots[0].Others; len(got) != maxOthers || got[0] != strings.Repeat("t", maxOthers+5) {
		t.Fatalf("%d other conversations kept, want the newest %d", len(got), maxOthers)
	}
}

// A Bot may work in other projects besides its own: absolute paths, each
// once, never its own again. Its conversations there are its own too.
func TestOtherProjects(t *testing.T) {
	b := Bot{Name: "Ops", Agent: "codex", Project: "/work/relay", Projects: []string{" /work/subapi ", "/work/relay", "/work/subapi", ""}}
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	if len(b.Projects) != 1 || b.Projects[0] != "/work/subapi" {
		t.Fatalf("projects = %q", b.Projects)
	}
	if !b.WorksIn("/work/relay") || !b.WorksIn("/work/subapi") || b.WorksIn("/work/other") || b.WorksIn("") {
		t.Fatal("WorksIn")
	}
	if bad := (Bot{Name: "A", Agent: "codex", Project: "/work/a", Projects: []string{"relative"}}); bad.Normalize() == nil {
		t.Fatal("a relative project was accepted")
	}
	var s Store
	saved, _ := s.Put(b)
	s.AddOther(saved.ID, "task-in-subapi")
	if _, ok := s.Match("codex", "/work/subapi", "task-in-subapi"); !ok {
		t.Fatal("a conversation in another project of the Bot did not match")
	}
	if text := Charter(saved); !strings.Contains(text, "/work/relay, /work/subapi") {
		t.Fatalf("the charter does not name the projects: %q", text)
	}
	if text := Charter(Bot{Name: "Solo", Project: "/work/a"}); strings.Contains(text, "these projects") {
		t.Fatalf("a Bot of one project lists projects: %q", text)
	}
}

// A Bot posts what it did on its own: one report per conversation, newest
// first, a bounded number, read once the person sees them. An edit from a
// window keeps them.
func TestReports(t *testing.T) {
	var s Store
	b, _ := s.Put(valid("Ops"))
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	if s.AddReport(b.ID, Report{Title: "no conversation"}) || s.AddReport("missing", Report{Session: "s"}) {
		t.Fatal("a report without a conversation or a Bot was posted")
	}
	s.AddReport(b.ID, Report{Session: "s1", Title: "first", At: at})
	s.AddReport(b.ID, Report{Session: "s2", Title: "second", At: at})
	s.AddReport(b.ID, Report{Session: "s1", Title: "first again", At: at})
	got := s.Bots[0].Reports
	if len(got) != 2 || got[0].Title != "first again" || got[1].Title != "second" || s.Bots[0].Unread() != 2 {
		t.Fatalf("reports = %+v", got)
	}
	if !s.ReadReports(b.ID) || s.ReadReports(b.ID) || s.Bots[0].Unread() != 0 {
		t.Fatal("ReadReports should mark them read once")
	}
	edit := s.Bots[0]
	edit.Reports = nil
	if kept, _ := s.Put(edit); len(kept.Reports) != 2 {
		t.Fatalf("an edit dropped the reports: %+v", kept.Reports)
	}
	for i := range maxReports + 3 {
		s.AddReport(b.ID, Report{Session: strings.Repeat("x", i+1), Title: "t"})
	}
	if n := len(s.Bots[0].Reports); n != maxReports {
		t.Fatalf("%d reports kept, want %d", n, maxReports)
	}
	if got := Summary(strings.Repeat("字", 400)); len([]rune(got)) != summaryRunes+1 {
		t.Fatalf("summary has %d runes", len([]rune(got)))
	}
}
