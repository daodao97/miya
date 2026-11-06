package quota

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"miya/internal/agent/quota"
	"miya/internal/agent/quota/testcli"
)

func TestCodexResetCountRemainsAuthoritativeWithPartialDetails(t *testing.T) {
	r, err := readCodex(context.Background(), testcli.Helper(t, "codex-resets"), t.TempDir())
	if err != nil || r.Resets == nil || r.Resets.AvailableCount != 3 || len(r.Resets.Credits) != 2 || r.Resets.Credits[0].ID != "first" {
		t.Fatalf("partial reset details changed count or ordering: %+v %v", r.Resets, err)
	}
	r, err = readCodex(context.Background(), testcli.Helper(t, "codex"), t.TempDir())
	if err != nil || r.Resets != nil {
		t.Fatal("missing reset field became zero credits", err)
	}
}

func TestCodexResetConsumptionRefreshesAndDoesNotRetry(t *testing.T) {
	for _, outcome := range []string{"reset", "alreadyRedeemed", "noCredit", "nothingToReset"} {
		t.Run(outcome, func(t *testing.T) {
			root := t.TempDir()
			log := root + "/requests.jsonl"
			t.Setenv("USAGE_RESET_LOG", log)
			t.Setenv("USAGE_RESET_OUTCOME", outcome)
			a := quota.ResetAttempt{Key: "one-logical-operation", CreditID: "first", AccountKey: "id:fixture", Home: root}
			out, err := useCodexReset(context.Background(), testcli.Helper(t, "reset-success"), root, a)
			if err != nil || out.Outcome != outcome || len(out.Reading.Windows) != 1 || out.Reading.Windows[0].Used != 0 || out.Reading.Resets.AvailableCount != 1 {
				t.Fatalf("wrong outcome or stale quota: %+v %v", out, err)
			}
			data, _ := os.ReadFile(log)
			var params map[string]string
			if json.Unmarshal(data, &params) != nil || !reflect.DeepEqual(params, map[string]string{"idempotencyKey": a.Key, "creditId": a.CreditID}) {
				t.Fatalf("wrong consume payload: %s", data)
			}
		})
	}
}

func TestCodexResetRejectsAccountSwitchAndPreservesAmbiguousAttempt(t *testing.T) {
	root := t.TempDir()
	log := root + "/requests.jsonl"
	t.Setenv("USAGE_RESET_LOG", log)
	a := quota.ResetAttempt{Key: "same-logical-operation", CreditID: "first", AccountKey: "id:fixture", Home: root}
	if _, err := useCodexReset(context.Background(), testcli.Helper(t, "reset-switched"), root, a); err == nil {
		t.Fatal("account switch was not rejected")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("account switch reached consume")
	}
	for range 2 {
		if _, err := useCodexReset(context.Background(), testcli.Helper(t, "reset-ambiguous"), root, a); err == nil {
			t.Fatal("ambiguous consume looked successful")
		}
	}
	data, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || lines[0] != lines[1] {
		t.Fatalf("retry changed idempotency payload: %s", data)
	}
}

func TestCodexResetConfirmedOutcomeSurvivesRefreshFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("USAGE_RESET_LOG", root+"/requests.jsonl")
	out, err := useCodexReset(context.Background(), testcli.Helper(t, "reset-refresh-failed"), root, quota.ResetAttempt{Key: "one-operation", AccountKey: "id:fixture", Home: root})
	if err != nil || out.Outcome != "reset" || out.Reading.Error == "" {
		t.Fatalf("confirmed use became retryable after a read failure: %+v %v", out, err)
	}
}
