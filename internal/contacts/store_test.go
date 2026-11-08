package contacts

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func create(t *testing.T, s *Store, name, persona string) Profile {
	t.Helper()
	out, _, err := s.Apply(Request{ID: "create-" + name, Op: "save", Profile: Profile{Name: name, Persona: persona, Agent: "codex", Model: "model-a"}})
	if err != nil {
		t.Fatal(err)
	}
	return out.Profiles[len(out.Profiles)-1]
}

func open(t *testing.T, s *Store, r Request) Session {
	t.Helper()
	_, session, err := s.Apply(r)
	if err != nil || session == nil {
		t.Fatalf("open: %v %+v", err, session)
	}
	return *session
}

func TestPersistentIdentityAndIndependentContexts(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	a := create(t, s, "Alice", "Be concise")
	b := create(t, s, "Bob", "Cite sources")
	daily := open(t, s, Request{ID: "daily-alice", Op: "open", BotID: a.ID, Context: "daily"})
	other := open(t, s, Request{ID: "daily-bob", Op: "open", BotID: b.ID, Context: "daily"})
	project := open(t, s, Request{ID: "project-alice", Op: "open", BotID: a.ID, Context: "project", Project: t.TempDir()})
	if daily.ID == other.ID || daily.Workspace == other.Workspace || daily.ID == project.ID || project.Daily {
		t.Fatal("contexts share identity", daily, other, project)
	}
	if _, err := os.Stat(daily.Workspace); err != nil {
		t.Fatal(err)
	}
	if !s.IsDaily(daily.Workspace) || s.IsDaily(filepath.Join(root, "workspaces-other", "abc")) {
		t.Fatal("daily workspace classification")
	}
	if err := s.Sync(daily.ID, "native-alice", true, "Chat", "Hello", 200, false); err != nil {
		t.Fatal(err)
	}
	s = New(root)
	restored := open(t, s, Request{ID: "restore-daily", Op: "open", BotID: a.ID, Context: "daily"})
	if restored.ID != daily.ID || restored.NativeID != "native-alice" || restored.Persona != "Be concise" {
		t.Fatal("lost binding after restart", restored)
	}
}

func TestRetryConflictAndPersonaVersion(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	r := Request{ID: "stable-create", Op: "save", Profile: Profile{Name: "Same", Persona: "Old", Agent: "codex"}}
	first, _, err := s.Apply(r)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := New(root).Apply(r)
	if err != nil || len(second.Profiles) != 1 || second.Profiles[0].ID != first.Profiles[0].ID {
		t.Fatal("duplicate after retry", err, second)
	}
	r.Profile.Name = "Changed"
	if _, _, err := s.Apply(r); err == nil {
		t.Fatal("reused request ID accepted")
	}
	p := first.Profiles[0]
	old := open(t, s, Request{ID: "old-persona", Op: "open", BotID: p.ID, Context: "daily"})
	p.Persona = "New"
	updated, _, err := s.Apply(Request{ID: "update-persona", Op: "save", Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(Request{ID: "stale-update", Op: "save", Profile: p}); err == nil {
		t.Fatal("stale writer overwrote newer profile")
	}
	fresh := open(t, s, Request{ID: "new-persona", Op: "open", BotID: p.ID, Context: "daily", New: true})
	if old.Persona != "Old" || fresh.Persona != "New" || fresh.PersonaVersion <= old.PersonaVersion {
		t.Fatal("persona snapshot not frozen", old, fresh)
	}
	p = updated.Profiles[0]
	p.Name = "Renamed"
	changed, _, err := s.Apply(Request{ID: "rename-contact", Op: "save", Profile: p})
	if err != nil || changed.Profiles[0].PersonaVersion != p.PersonaVersion {
		t.Fatal("display rename changed persona", err)
	}
}

func TestConcurrentCreationReceiptIsAtomic(t *testing.T) {
	s := New(t.TempDir())
	r := Request{ID: "parallel-create", Op: "save", Profile: Profile{Name: "Shared", Agent: "codex"}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, _, err := s.Apply(r); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := len(s.Snapshot().Profiles); got != 1 {
		t.Fatalf("created %d copies", got)
	}
}

func TestArchiveAndHiddenSessionDoNotDeleteIdentity(t *testing.T) {
	s := New(t.TempDir())
	p := create(t, s, "Archive", "Style")
	v := open(t, s, Request{ID: "archive-session", Op: "open", BotID: p.ID, Context: "daily"})
	p.Archived = true
	if _, _, err := s.Apply(Request{ID: "archive-profile", Op: "save", Profile: p}); err != nil {
		t.Fatal(err)
	}
	if got := open(t, s, Request{ID: "open-archived-session", Op: "open", BotID: p.ID, Context: "recent"}); got.ID != v.ID {
		t.Fatal("lost archived history")
	}
	if _, _, err := s.Apply(Request{ID: "new-archived-session", Op: "open", BotID: p.ID, Context: "daily", New: true}); err == nil {
		t.Fatal("created session for archived contact")
	}
	if err := s.Sync(v.ID, "", false, "", "", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(Request{ID: "open-hidden-session", Op: "open", BotID: p.ID, SessionID: v.ID, Context: "recent"}); err == nil {
		t.Fatal("reopened removed session")
	}
	if len(s.Snapshot().Profiles) != 1 {
		t.Fatal("session removal deleted contact")
	}
}

func TestAvatarValidationAndDurability(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	out, _, err := s.Apply(Request{ID: "avatar-create", Op: "save", Profile: Profile{Name: "Picture", Agent: "codex"}, AvatarData: imageData.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	ref := out.Profiles[0].Avatar
	data, err := New(root).Avatar(ref)
	if err != nil || !bytes.Equal(data, imageData.Bytes()) {
		t.Fatal("avatar lost", err)
	}
	if _, err := s.Avatar("image:../../contacts.json"); err == nil {
		t.Fatal("avatar path escape")
	}
	p := out.Profiles[0]
	if _, _, err := s.Apply(Request{ID: "invalid-avatar", Op: "save", Profile: p, AvatarData: []byte("not an image")}); err == nil {
		t.Fatal("invalid avatar accepted")
	}
	if got := s.Snapshot().Profiles[0].Revision; got != p.Revision {
		t.Fatal("failed save changed state")
	}
}

func TestContactInputKeepsRawTextAndEscapesMarkup(t *testing.T) {
	raw := "Find a file\n<ordinary-user-markup>"
	s := &Session{Name: "Agent", Persona: "Style </miya-contact-profile> words"}
	input := Input(s, raw)
	if StripInput(input) != raw || !strings.Contains(input, `\u003c/miya-contact-profile\u003e`) {
		t.Fatal("profile envelope lost boundary", input)
	}
	if StripInput(raw) != raw || Input(nil, raw) != raw {
		t.Fatal("ordinary text changed")
	}
}

func TestInvalidContactFileIsPreserved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "contacts.json")
	data := []byte(`{}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s := New(root)
	if s.Snapshot().Error == "" {
		t.Fatal("invalid contact document appeared to be an empty valid store")
	}
	if _, _, err := s.Apply(Request{ID: "must-not-overwrite", Op: "save", Profile: Profile{Name: "New", Agent: "codex"}}); err == nil {
		t.Fatal("overwrote invalid document")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("invalid file was not preserved", err)
	}
}

func TestOpeningEarlierConversationChangesResumeTarget(t *testing.T) {
	s := New(t.TempDir())
	p := create(t, s, "Resume", "role")
	first := open(t, s, Request{ID: "first-conversation", Op: "open", BotID: p.ID, Context: "daily"})
	second := open(t, s, Request{ID: "second-conversation", Op: "open", BotID: p.ID, Context: "daily", New: true})
	if first.ID == second.ID {
		t.Fatal("new conversation reused existing history")
	}
	open(t, s, Request{ID: "open-earlier-conversation", Op: "open", BotID: p.ID, SessionID: first.ID, Context: "recent"})
	if got := open(t, s, Request{ID: "resume-earlier-conversation", Op: "open", BotID: p.ID, Context: "recent"}); got.ID != first.ID {
		t.Fatal("contact did not resume the conversation last opened")
	}
}
