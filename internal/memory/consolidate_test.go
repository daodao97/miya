package memory

import (
	"errors"
	"testing"
	"time"
)

func later(s *Store, d time.Duration) {
	now := s.now()
	s.now = func() time.Time { return now.Add(d) }
}

func consolidating(t *testing.T) (*Store, Scope) {
	t.Helper()
	s, scope := fixture(t)
	if err := s.Configure(scope, Settings{AutoExtract: true}); err != nil {
		t.Fatal(err)
	}
	return s, scope
}

func claimConsolidation(t *testing.T, s *Store) ConsolidationJob {
	t.Helper()
	_, job, ok, err := s.ClaimConsolidation()
	if err != nil || !ok {
		t.Fatalf("consolidation not claimed: %v", err)
	}
	return job
}

func latestByID(t *testing.T, s *Store, scope Scope) map[string]Entry {
	t.Helper()
	entries, _, _, err := s.List(scope)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Entry{}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out
}

func pair(a, b Entry) []ConsolidationTarget {
	return []ConsolidationTarget{{a.ID, a.Version}, {b.ID, b.Version}}
}

func TestConsolidationWaitsUntilAutomaticProjectIsQuiet(t *testing.T) {
	s, scope := fixture(t)
	save(t, s, scope, mutation(s, "手机构建使用 build-ios.sh"))
	save(t, s, scope, mutation(s, "发布前运行 go test"))
	later(s, ConsolidationIdle)
	if _, _, ok, _ := s.ClaimConsolidation(); ok {
		t.Fatal("consolidated a project without automatic extraction")
	}
	if err := s.Configure(scope, Settings{AutoExtract: true}); err != nil {
		t.Fatal(err)
	}
	save(t, s, scope, mutation(s, "发布说明写中文"))
	if _, _, ok, _ := s.ClaimConsolidation(); ok {
		t.Fatal("consolidated right after a memory changed")
	}
	later(s, ConsolidationIdle)
	job := claimConsolidation(t, s)
	if len(job.Entries) != 3 || job.ExtractorAgent != "claude-code" || job.Key == "" {
		t.Fatalf("job: %+v", job)
	}
	if _, _, ok, _ := s.ClaimConsolidation(); ok {
		t.Fatal("claimed a running consolidation twice")
	}
}

func TestConsolidationMergeKeepsOldestIDAndChecksEachOperation(t *testing.T) {
	s, scope := consolidating(t)
	a := save(t, s, scope, mutation(s, "手机构建使用 build-ios.sh"))
	later(s, time.Minute)
	b := save(t, s, scope, mutation(s, "iOS 构建脚本固定为 build-ios.sh"))
	c := save(t, s, scope, mutation(s, "发布前运行 go test ./..."))
	pm := mutation(s, "回复使用中文")
	pm.Kind = "preference"
	p := save(t, s, scope, pm)
	later(s, ConsolidationIdle)
	job := claimConsolidation(t, s)
	merged := "手机（iOS）构建固定使用 build-ios.sh"
	ops := []ConsolidationOp{
		{Action: "merge", Targets: pair(b, a), Content: merged},
		{Action: "merge", Targets: pair(c, p), Content: "发布前运行 go test ./..."},
		{Action: "merge", Targets: pair(c, a), Content: "发布前运行 go test ./..."},
		{Action: "merge", Targets: []ConsolidationTarget{{c.ID, c.Version}, {"missing", 1}}, Content: "发布前运行 go test ./..."},
		{Action: "delete", Targets: pair(c, p)},
	}
	written, err := s.CompleteConsolidation(scope, job.Key, ops, time.Second, nil, false)
	if err != nil || len(written) != 2 {
		t.Fatalf("written: %v %v", written, err)
	}
	got := latestByID(t, s, scope)
	if e := got[a.ID]; e.Version != 2 || e.Content != merged || e.State != EntryActive || len(e.Sources) != 2 {
		t.Fatalf("kept entry: %+v", e)
	}
	if e := got[b.ID]; e.State != EntryMerged || e.MergedInto != a.ID || e.Content == "" {
		t.Fatalf("merged entry: %+v", e)
	}
	if got[c.ID].Version != 1 || got[p.ID].Version != 1 {
		t.Fatal("rejected operations changed memories")
	}
	if found, _ := s.Search(scope, "build-ios.sh", "", 5); len(found) != 1 || found[0].ID != a.ID {
		t.Fatalf("merged entry still recalled: %v", found)
	}
	d, _ := s.load(scope)
	if d.Consolidation.Rejected != 4 || d.Consolidation.State != "" || len(d.Consolidation.Offered) != 0 {
		t.Fatalf("state: %+v", d.Consolidation)
	}
	if status, _ := s.ExtractionStatus(scope); status.Merged != 1 || status.ConsolidatedAt.IsZero() || status.ConsolidationError != "" {
		t.Fatalf("status: %+v", status)
	}
	later(s, 2*ConsolidationIdle)
	if _, _, ok, _ := s.ClaimConsolidation(); ok {
		t.Fatal("unchanged library consolidated again")
	}
}

func TestConsolidationRejectsInventedIdentifiersAndStaleTargets(t *testing.T) {
	s, scope := consolidating(t)
	a := save(t, s, scope, mutation(s, "构建脚本是 build-ios.sh"))
	b := save(t, s, scope, mutation(s, "iOS 构建用 build-ios.sh 脚本"))
	c := save(t, s, scope, mutation(s, "发布前跑 go test"))
	d := save(t, s, scope, mutation(s, "发布前要跑 go test"))
	later(s, ConsolidationIdle)
	job := claimConsolidation(t, s)
	// An explicit correction while the model runs wins over its proposal.
	fix := mutation(s, "发布前跑 go test ./internal/...")
	fix.ID, fix.Version = c.ID, c.Version
	save(t, s, scope, fix)
	ops := []ConsolidationOp{
		{Action: "merge", Targets: pair(a, b), Content: "构建脚本是 build-android.sh"},
		{Action: "merge", Targets: pair(c, d), Content: "发布前跑 go test"},
		{Action: "conflict", Targets: pair(a, b), Reason: "脚本说法不一致"},
	}
	written, err := s.CompleteConsolidation(scope, job.Key, ops, time.Second, nil, false)
	if err != nil || len(written) != 2 {
		t.Fatalf("written: %v %v", written, err)
	}
	got := latestByID(t, s, scope)
	if got[a.ID].State != EntryConflicted || got[b.ID].State != EntryConflicted {
		t.Fatal("conflict not marked after a rejected merge of the same entries")
	}
	if got[c.ID].Version != 2 || got[c.ID].Content != fix.Content || got[d.ID].State != EntryActive {
		t.Fatal("stale merge overwrote an explicit correction")
	}
	if found, _ := s.Search(scope, "build-ios.sh", "", 5); len(found) != 0 {
		t.Fatalf("conflicted entries recalled: %v", found)
	}
}

func TestForgettingMergedMemoryForgetsEntriesMergedIntoIt(t *testing.T) {
	s, scope := consolidating(t)
	a := save(t, s, scope, mutation(s, "内部域名是 corp.example"))
	later(s, time.Minute)
	b := save(t, s, scope, mutation(s, "公司内部域名 corp.example"))
	later(s, ConsolidationIdle)
	job := claimConsolidation(t, s)
	if _, err := s.CompleteConsolidation(scope, job.Key, []ConsolidationOp{{Action: "merge", Targets: pair(a, b), Content: "公司内部域名是 corp.example"}}, time.Second, nil, false); err != nil {
		t.Fatal(err)
	}
	kept := latestByID(t, s, scope)[a.ID]
	if _, err := s.Forget(scope, kept.ID, kept.Version); err != nil {
		t.Fatal(err)
	}
	for id, e := range latestByID(t, s, scope) {
		if e.State != EntryForgotten || e.Content != "" {
			t.Fatalf("%s survived forgetting: %+v", id, e)
		}
	}
	d, _ := s.load(scope)
	if len(d.Tombstones) != 2 {
		t.Fatalf("tombstones: %d", len(d.Tombstones))
	}
}

func TestConsolidationFailureRetriesThenWaitsForChange(t *testing.T) {
	s, scope := consolidating(t)
	save(t, s, scope, mutation(s, "构建脚本是 build-ios.sh"))
	save(t, s, scope, mutation(s, "发布前跑 go test"))
	later(s, ConsolidationIdle)
	job := claimConsolidation(t, s)
	if _, err := s.CompleteConsolidation(scope, job.Key, nil, time.Second, errors.New("cli broke"), false); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.ExtractionStatus(scope); status.ConsolidationError == "" || status.ConsolidationDetail != "cli broke" {
		t.Fatalf("status: %+v", status)
	}
	if _, _, ok, _ := s.ClaimConsolidation(); ok {
		t.Fatal("retried without waiting")
	}
	later(s, consolidationRetryAfter)
	job = claimConsolidation(t, s)
	if _, err := s.CompleteConsolidation(scope, job.Key, nil, time.Second, errors.New("cli broke"), false); err != nil {
		t.Fatal(err)
	}
	later(s, ConsolidationIdle)
	if _, _, ok, _ := s.ClaimConsolidation(); ok {
		t.Fatal("kept retrying an unchanged library")
	}
	save(t, s, scope, mutation(s, "回复使用中文"))
	later(s, ConsolidationIdle)
	claimConsolidation(t, s)
}

func TestConsolidationRecoversAndRequeuesInterruptedRuns(t *testing.T) {
	s, scope := consolidating(t)
	a := save(t, s, scope, mutation(s, "构建脚本是 build-ios.sh"))
	b := save(t, s, scope, mutation(s, "iOS 构建用 build-ios.sh"))
	later(s, ConsolidationIdle)
	old := claimConsolidation(t, s)
	if err := s.RecoverExtractions(); err != nil {
		t.Fatal(err)
	}
	job := claimConsolidation(t, s)
	if written, _ := s.CompleteConsolidation(scope, old.Key, []ConsolidationOp{{Action: "conflict", Targets: pair(a, b)}}, time.Second, nil, false); len(written) != 0 {
		t.Fatal("a result of an interrupted run was applied")
	}
	if _, err := s.CompleteConsolidation(scope, job.Key, nil, 0, errors.New("stopping"), true); err != nil {
		t.Fatal(err)
	}
	claimConsolidation(t, s)
	d, _ := s.load(scope)
	if d.Consolidation.Attempts != 2 || d.Consolidation.Error != "" {
		t.Fatalf("requeue counted as a failure: %+v", d.Consolidation)
	}
}

func TestMergedMemoryInvalidatesSessionsThatRecalledIt(t *testing.T) {
	s, scope := fixture(t)
	if err := s.Configure(scope, Settings{AutoExtract: true, AutoInject: true}); err != nil {
		t.Fatal(err)
	}
	a := save(t, s, scope, mutation(s, "手机构建使用 build-ios.sh"))
	later(s, time.Minute)
	b := save(t, s, scope, mutation(s, "iOS 构建脚本固定为 build-ios.sh"))
	first, err := s.Prepare(scope, "session", "epoch", "build-ios.sh", "")
	if err != nil || len(first.Entries) != 2 {
		t.Fatalf("first recall: %+v %v", first, err)
	}
	if err := s.FinishPrepared(scope, first, true); err != nil {
		t.Fatal(err)
	}
	later(s, ConsolidationIdle)
	job := claimConsolidation(t, s)
	if _, err := s.CompleteConsolidation(scope, job.Key, []ConsolidationOp{{Action: "merge", Targets: pair(a, b), Content: "手机（iOS）构建固定使用 build-ios.sh"}}, time.Second, nil, false); err != nil {
		t.Fatal(err)
	}
	next, err := s.Prepare(scope, "session", "epoch", "build-ios.sh", "")
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]Stamp{}
	for _, v := range next.Entries {
		states[v.ID] = v
	}
	if states[b.ID].State != EntryMerged || states[a.ID].Version != 2 {
		t.Fatalf("session not told about the merge: %+v", next.Entries)
	}
}

func TestIdentifierTokens(t *testing.T) {
	got := identifierTokens("运行 build-ios.sh，在 connectService 里看 v1.2 和 MIYA_ISOLATED_DIR，不是 Upgrade 或 cross-project。")
	want := []string{"build-ios.sh", "connectService", "v1.2", "MIYA_ISOLATED_DIR"}
	if len(got) != len(want) {
		t.Fatalf("tokens: %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokens: %q", got)
		}
	}
}
