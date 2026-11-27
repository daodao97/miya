package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Bot's memory is its own: searched and recalled only in that Bot's
// conversations, together with the project's, and kept in its own file.
func TestBotMemoryIsTheBotsOwn(t *testing.T) {
	s, project := fixture(t)
	rin := BotScope(project, "a1b2c3d4")
	for _, bad := range []Scope{
		BotScope(project, ""),
		BotScope(project, "../x"),
		BotScope(project, "ABC"),
		{Kind: "bot", Bot: "a1", ProjectPath: "relative"},
		{Kind: "project", ProjectKey: project.ProjectKey, ProjectPath: project.ProjectPath, Bot: "a1"},
	} {
		if _, err := s.Save(bad, mutation(s, "x")); err == nil {
			t.Errorf("scope %+v accepted", bad)
		}
	}
	mine := save(t, s, rin, mutation(s, "发布前先跑 go vet"))
	save(t, s, project, mutation(s, "发布用 release.sh"))
	if err := s.Configure(project, Settings{AutoInject: true}); err != nil {
		t.Fatal(err)
	}

	if got, err := s.Search(project, "发布", "", 5); err != nil || len(got) != 1 {
		t.Fatalf("the project's search found %d entries, %v; want its own one", len(got), err)
	}
	if got, err := s.SearchBot(project, rin, "发布", "", 5); err != nil || len(got) != 2 {
		t.Fatalf("Rin's search found %d entries, %v; want the project's and its own", len(got), err)
	}
	if got, _ := s.SearchBot(project, BotScope(project, "0f0f0f0f"), "发布", "", 5); len(got) != 1 {
		t.Fatalf("another Bot's search found %d entries, want the project's alone", len(got))
	}

	p, err := s.PrepareBot(project, rin, false, "rin-session", "1", "发布", "")
	if err != nil || !strings.Contains(p.Block, mine.Content) || !s.ValidatePrepared(project, p) {
		t.Fatalf("Rin's recall: %+v %v", p, err)
	}
	if other, _ := s.PrepareBot(project, BotScope(project, "0f0f0f0f"), false, "kai-session", "1", "发布", ""); strings.Contains(other.Block, mine.Content) {
		t.Fatal("another Bot recalled Rin's memory")
	}
	if plain, _ := s.Prepare(project, "plain-session", "1", "发布", ""); strings.Contains(plain.Block, mine.Content) {
		t.Fatal("a conversation of no Bot recalled Rin's memory")
	}
	if _, err := os.Stat(filepath.Join(s.root, "bots", project.ProjectKey+"-a1b2c3d4.json")); err != nil {
		t.Fatalf("Rin's memory file: %v", err)
	}

	if _, err := s.Forget(rin, mine.ID, mine.Version); err != nil {
		t.Fatal(err)
	}
	if s.ValidatePrepared(project, p) {
		t.Fatal("a retry kept a forgotten Bot memory")
	}
	if got, _ := s.SearchBot(project, rin, "go vet", "", 5); len(got) != 0 {
		t.Fatalf("a forgotten Bot memory is still found: %v", got)
	}
}

// A Bot keeps a bounded memory: a full one takes corrections and forgetting,
// and a new entry only once one is gone. The project's memory has no such
// bound.
func TestBotMemoryIsBounded(t *testing.T) {
	s, project := fixture(t)
	rin := BotScope(project, "a1")
	var first Entry
	for i := range MaxBotEntries {
		e := save(t, s, rin, mutation(s, fmt.Sprintf("经验 %d", i)))
		if i == 0 {
			first = e
		}
	}
	if _, err := s.Save(rin, mutation(s, "one more")); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("a full Bot memory took another entry: %v", err)
	}
	m := mutation(s, "经验 0，修订后")
	m.ID, m.Version = first.ID, first.Version
	corrected, err := s.Save(rin, m)
	if err != nil {
		t.Fatalf("a full Bot memory refused a correction: %v", err)
	}
	if _, err := s.Forget(rin, corrected.ID, corrected.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(rin, mutation(s, "one more")); err != nil {
		t.Fatalf("a Bot memory with room refused an entry: %v", err)
	}
	for i := range MaxBotEntries + 1 {
		save(t, s, project, mutation(s, fmt.Sprintf("事实 %d", i)))
	}
}

// A Bot recalls its own memory without the project's recall, and in its
// tasks recalls only its own; memory turned off recalls nothing.
func TestBotRecallsItsOwnMemory(t *testing.T) {
	s, project := fixture(t)
	rin := BotScope(project, "a1")
	mine := save(t, s, rin, mutation(s, "发布前先跑 go vet"))
	theirs := save(t, s, project, mutation(s, "发布用 release.sh"))
	p, err := s.PrepareBot(project, rin, false, "talk", "1", "发布", "")
	if err != nil || !strings.Contains(p.Block, mine.Content) || strings.Contains(p.Block, theirs.Content) || !s.ValidatePrepared(project, p) {
		t.Fatalf("without the project's recall: %+v %v", p, err)
	}
	if err := s.Configure(project, Settings{AutoInject: true}); err != nil {
		t.Fatal(err)
	}
	if task, _ := s.PrepareBot(project, rin, true, "task", "1", "发布", ""); !strings.Contains(task.Block, mine.Content) || strings.Contains(task.Block, theirs.Content) {
		t.Fatalf("a task recalled %q", task.Block)
	}
	if plain, _ := s.Prepare(project, "plain", "1", "发布", ""); !strings.Contains(plain.Block, theirs.Content) {
		t.Fatal("the project's recall stopped")
	}
	if err := s.Configure(project, Settings{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if off, _ := s.PrepareBot(project, rin, false, "talk2", "1", "发布", ""); off.Block != "" {
		t.Fatal("memory turned off still recalled the Bot's")
	}
}
