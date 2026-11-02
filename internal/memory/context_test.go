package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAutomaticRecallDeltaCorrectionForgetAndRestart(t *testing.T) {
	s, scope := fixture(t)
	e := save(t, s, scope, mutation(s, "手机协议暂缓升级"))
	if err := s.Configure(scope, Settings{AutoInject: true}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Prepare(scope, "codex-session", "1", "手机协议", "")
	if err != nil || !strings.Contains(p.Block, e.Content) || !s.ValidatePrepared(scope, p) {
		t.Fatalf("initial recall: %+v %v", p, err)
	}
	if StripContext("原文"+p.Block) != "原文" {
		t.Fatal("injected history leaked")
	}
	_ = s.FinishPrepared(scope, p, true)
	duplicate, _ := s.Prepare(scope, "codex-session", "1", "手机协议", "")
	if duplicate.Block != "" {
		t.Fatal("unchanged memory repeated")
	}
	m := mutation(s, "手机协议允许升级")
	m.ID, m.Version = e.ID, e.Version
	e = save(t, s, scope, m)
	if s.ValidatePrepared(scope, p) {
		t.Fatal("old retry accepted changed memory")
	}
	changed, _ := s.Prepare(scope, "codex-session", "1", "其他问题", "")
	if !strings.Contains(changed.Block, e.Content) {
		t.Fatal("changed version not corrected")
	}
	if _, err = s.Forget(scope, e.ID, e.Version); err != nil {
		t.Fatal(err)
	}
	if s.ValidatePrepared(scope, changed) {
		t.Fatal("forgotten retry accepted")
	}
	forgotten, _ := s.Prepare(scope, "codex-session", "1", "其他问题", "")
	if strings.Contains(forgotten.Block, e.Content) || !strings.Contains(forgotten.Block, "forgotten") {
		t.Fatal("forget invalidation missing or retained body")
	}
	restarted := New(s.root)
	next, _ := restarted.Prepare(scope, "codex-session", "1", "手机协议", "")
	if next.Block != "" {
		t.Fatal("restart repeated forgotten memory")
	}
	all, err := restarted.Injections(scope, "codex-session")
	if err != nil || len(all) != 5 {
		t.Fatalf("diagnostics: %d %v", len(all), err)
	}
}

func TestAutomaticRecallResetsFailureEpochAndBudgets(t *testing.T) {
	s, scope := fixture(t)
	for i := 0; i < 8; i++ {
		save(t, s, scope, mutation(s, fmt.Sprintf("协议 %d %s", i, strings.Repeat("正文", 600))))
	}
	_ = s.Configure(scope, Settings{AutoInject: true})
	p, err := s.Prepare(scope, "claude-session", "process-a", "协议", "")
	if err != nil || p.Block == "" || utf8.RuneCountInString(p.Block) > 4000 || len(p.Entries) > 5 {
		t.Fatalf("budget: %+v %v", p, err)
	}
	_ = s.FinishPrepared(scope, p, false)
	retry, _ := s.Prepare(scope, "claude-session", "process-a", "协议", "")
	if retry.Block == "" {
		t.Fatal("failed send consumed recall")
	}
	_ = s.FinishPrepared(scope, retry, true)
	epoch, _ := s.Prepare(scope, "claude-session", "process-b", "协议", "")
	if epoch.Block == "" {
		t.Fatal("new context did not replay")
	}
	for i := 0; i < 25; i++ {
		_, _ = s.Prepare(scope, "claude-session", "process-b", "协议", "")
	}
	all, _ := s.Injections(scope, "claude-session")
	if len(all) > 20 {
		t.Fatal("unbounded diagnostics")
	}
	other, _ := ProjectScope(t.TempDir())
	_ = s.Configure(other, Settings{AutoInject: true})
	leaked, _ := s.Prepare(other, "other-session", "", "协议", "")
	if leaked.Block != "" {
		t.Fatal("project isolation broken")
	}
	_ = s.Configure(scope, Settings{Disabled: true, AutoInject: true})
	disabled, _ := s.Prepare(scope, "claude-session", "process-b", "协议", "")
	if disabled.Block != "" {
		t.Fatal("disabled project recalled")
	}
}

func TestAutomaticRecallPreferenceRemovalAndExpiration(t *testing.T) {
	s, scope := fixture(t)
	m := mutation(s, "偏好中文解释协议")
	m.Kind = "preference"
	e := save(t, s, UserScope(), m)
	_ = s.Configure(scope, Settings{AutoInject: true, UseUserPreferences: true})
	p, _ := s.Prepare(scope, "session", "", "协议", "")
	if !strings.Contains(p.Block, e.Content) {
		t.Fatal("preference missing")
	}
	_ = s.Configure(scope, Settings{AutoInject: true})
	p, _ = s.Prepare(scope, "session", "", "协议", "")
	if !strings.Contains(p.Block, "unavailable") || !s.ValidatePrepared(scope, p) {
		t.Fatal("disabled preference invalidation missing")
	}
	m = mutation(s, "临时协议冻结")
	m.ValidUntil = s.now().Add(time.Hour)
	save(t, s, scope, m)
	p, _ = s.Prepare(scope, "session", "", "协议", "")
	if !strings.Contains(p.Block, m.Content) {
		t.Fatal("temporary memory missing")
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(2 * time.Hour) }
	if s.ValidatePrepared(scope, p) {
		t.Fatal("expired snapshot accepted")
	}
	p, _ = s.Prepare(scope, "session", "", "协议", "")
	if !strings.Contains(p.Block, "expired") {
		t.Fatal("expiry not signaled")
	}
}

func TestAutomaticRecallThousandEntryPreparationLatency(t *testing.T) {
	s, scope := fixture(t)
	d, _ := s.load(scope)
	for i := 0; i < 1000; i++ {
		m := mutation(s, fmt.Sprintf("手机构建约定 %d：%s", i, strings.Repeat("固定入口需要检查", 20)))
		d.Entries = append(d.Entries, Entry{ID: fmt.Sprintf("%032d", i), Version: 1, Scope: scope, Kind: m.Kind, Content: m.Content, Sources: m.Sources, ObservedAt: m.ObservedAt, CreatedAt: s.now(), UpdatedAt: s.now(), State: "active"})
	}
	d.Settings.AutoInject = true
	if err := s.commit(d); err != nil {
		t.Fatal(err)
	}
	var timings []float64
	for i := 0; i < 25; i++ {
		start := time.Now()
		p, err := s.Prepare(scope, fmt.Sprintf("session-%d", i), "", "手机构建", "")
		if err != nil || p.Block == "" || !s.ValidatePrepared(scope, p) {
			t.Fatalf("prepare: %v", err)
		}
		timings = append(timings, float64(time.Since(start).Microseconds())/1000)
	}
	sort.Float64s(timings)
	report := map[string]any{"entries": 1000, "samples": len(timings), "p50_ms": timings[len(timings)/2], "p95_ms": timings[23]}
	b, _ := json.Marshal(report)
	t.Log(string(b))
	if dir := os.Getenv("MIYA_MEMORY_REPORT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "recall-latency.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
