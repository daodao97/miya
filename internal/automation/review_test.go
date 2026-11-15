package automation

import (
	"strings"
	"testing"
	"time"
)

func bash(command string) Ask { return claudeAsk("Bash", map[string]string{"command": command}) }

// startIngress starts a run of id and returns its conversation.
func (w *wakeHarness) startIngress(id string) int {
	w.t.Helper()
	if wk, err := w.rn.Ingress(id, "", "", false, "ping", ""); err != nil || wk.Outcome != WakeStarted {
		w.t.Fatalf("not started: %+v %v", wk, err)
	}
	return w.rn.Active[id].Thread
}

func (w *wakeHarness) reviewNotices() int {
	n := 0
	for _, x := range w.h.notices {
		if strings.HasPrefix(x.ID, "miya-auto-review-") {
			n++
		}
	}
	return n
}

func externalAutomation(w *wakeHarness) Automation {
	auto := ingressAutomation(WakePolicy{})
	auto.Access = AccessExternal
	return w.add(auto)
}

// An outside action of an external automation goes to the person; once
// approved, the automation is woken and the same action is allowed once.
func TestReviewApprovedOnce(t *testing.T) {
	w := newWakeHarness(t)
	a := externalAutomation(w)
	thread := w.startIngress(a.ID)
	got := w.rn.Unattended(thread, bash("git push origin miya/deps"))
	if !got.Decided || got.Allow || !strings.Contains(got.Message, "for review as #") {
		t.Fatalf("push: %+v", got)
	}
	if again := w.rn.Unattended(thread, bash("git push origin miya/deps")); again.Message != got.Message {
		t.Fatalf("the same action made another review: %q", again.Message)
	}
	pending := w.rn.Store.PendingReviews(a.ID)
	if len(pending) != 1 || pending[0].Key != "git push" || pending[0].Instructions == "" || w.reviewNotices() != 1 {
		t.Fatalf("reviews %+v, notices %d", pending, w.reviewNotices())
	}
	w.endRun(a.ID)
	if err := w.rn.DecideReview(pending[0].ID, true, ScopeOnce, ""); err != nil {
		t.Fatal(err)
	}
	if w.rn.Active[a.ID] == nil {
		t.Fatal("the decision did not wake the automation")
	}
	if text := w.h.sent[len(w.h.sent)-1].Text; !strings.Contains(text, "approved once") || !strings.Contains(text, "git push origin miya/deps") {
		t.Fatalf("the run was not told:\n%s", text)
	}
	thread = w.rn.Active[a.ID].Thread
	if got := w.rn.Unattended(thread, bash("git push origin miya/deps")); !got.Decided || !got.Allow {
		t.Fatalf("approved action refused: %+v", got)
	}
	if got := w.rn.Unattended(thread, bash("git push origin miya/deps")); got.Allow {
		t.Fatal("a one-time approval allowed it twice")
	}
	if err := w.rn.DecideReview(pending[0].ID, false, "", ""); err == nil {
		t.Fatal("decided twice")
	}
	// The decision is told once.
	w.endRun(a.ID)
	w.now = w.now.Add(time.Minute)
	w.startIngress(a.ID)
	if text := w.h.sent[len(w.h.sent)-1].Text; strings.Contains(text, "approved once") {
		t.Fatal("the decision was told again")
	}
}

func TestReviewApprovedForTheKindUntilRevoked(t *testing.T) {
	w := newWakeHarness(t)
	a := externalAutomation(w)
	thread := w.startIngress(a.ID)
	w.rn.Unattended(thread, bash("gh pr create --fill"))
	id := w.rn.Store.PendingReviews(a.ID)[0].ID
	if err := w.rn.DecideReview(id, true, ScopeKind, ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"gh pr create --fill", "gh pr create --title other"} {
		if got := w.rn.Unattended(thread, bash(c)); !got.Allow {
			t.Fatalf("%s: %+v", c, got)
		}
	}
	if got := w.rn.Unattended(thread, bash("gh pr merge 3")); got.Allow {
		t.Fatal("another kind was allowed")
	}
	if len(w.rn.Store.GrantsOf(a.ID, w.now)) != 1 || !w.rn.RevokeGrant(id) {
		t.Fatalf("grants: %+v", w.rn.Store.Grants)
	}
	if got := w.rn.Unattended(thread, bash("gh pr create --fill")); got.Allow {
		t.Fatal("allowed after revoking")
	}
	// A grant ends after a week.
	w.rn.Unattended(thread, bash("docker push img"))
	var docker string
	for _, r := range w.rn.Store.PendingReviews(a.ID) {
		if r.Key == "docker push" {
			docker = r.ID
		}
	}
	_ = w.rn.DecideReview(docker, true, ScopeKind, "")
	w.now = w.now.Add(8 * 24 * time.Hour)
	if got := w.rn.Unattended(thread, bash("docker push img")); got.Allow {
		t.Fatal("allowed after the grant expired")
	}
}

func TestReviewDeniedAndExpired(t *testing.T) {
	w := newWakeHarness(t)
	a := externalAutomation(w)
	thread := w.startIngress(a.ID)
	w.rn.Unattended(thread, bash("npm publish"))
	w.rn.Unattended(thread, bash("curl -X POST https://hooks.example/x"))
	w.endRun(a.ID)
	// A stalled automation still hears a decision.
	w.rn.Store.Stalled = map[string]time.Time{a.ID: w.now}
	reviews := w.rn.Store.PendingReviews(a.ID)
	if err := w.rn.DecideReview(reviews[0].ID, false, "", "Not before the release notes"); err != nil {
		t.Fatal(err)
	}
	if w.rn.Active[a.ID] == nil {
		t.Fatalf("a stalled automation was not woken: %+v", w.rn.Store.Wakes[a.ID])
	}
	if text := w.h.sent[len(w.h.sent)-1].Text; !strings.Contains(text, "denied") || !strings.Contains(text, "Not before the release notes") {
		t.Fatalf("the run was not told:\n%s", text)
	}
	// A decision while a run goes on waits for it.
	w.now = w.now.Add(8 * 24 * time.Hour)
	w.rn.Tick()
	if r := w.rn.Store.Reviews[w.rn.findReview(reviews[1].ID)]; r.State != ReviewExpired {
		t.Fatalf("not expired: %+v", r)
	}
	if wakes := w.rn.Store.Wakes[a.ID]; wakes[len(wakes)-1].Outcome != WakeQueued {
		t.Fatalf("the expiry wake was not queued: %+v", wakes[len(wakes)-1])
	}
	w.endRun(a.ID)
	w.rn.Tick()
	if text := w.h.sent[len(w.h.sent)-1].Text; !strings.Contains(text, "did not decide within 7 days") {
		t.Fatalf("the next run was not told of the expiry:\n%s", text)
	}
}

func TestActionKey(t *testing.T) {
	cases := map[string]string{
		"git push origin main":           "git push",
		"go test ./... && git -C x push": "git push",
		"gh pr create --fill":            "gh pr create",
		"gh -R o/r issue comment 3":      "gh issue comment",
		"curl -X POST https://x":         "curl",
		"ssh host deploy":                "ssh",
		"Edit /etc/hosts":                "Edit /etc/hosts",
	}
	for in, want := range cases {
		if got := ActionKey(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
