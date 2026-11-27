package controller

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/interaction"
	"miya/internal/uiruntime"
)

// A pending review waits among the approvals, in the conversation of the
// run that asked, without holding that run up; answering the card decides
// the review.
func TestReviewsWaitAmongApprovals(t *testing.T) {
	c := &Controller{}
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{{ID: 1, ServerID: "t1", Title: "Deps · run"}})
	c.Status, c.SetStatus = uiruntime.CreateSignal("")
	c.Auto = automation.NewRunner(automationHost{c}, filepath.Join(t.TempDir(), "automations.json"))
	// The project is gone, so the run a decision wakes ends at once.
	c.Auto.Store.Automations = []automation.Automation{{ID: "a1", Name: "Deps", Project: filepath.Join(t.TempDir(), "gone"), Instructions: "push it", Access: automation.AccessExternal, Rounds: 1}}
	now := time.Now()
	c.Auto.Store.Reviews = []automation.Review{
		{ID: "r1", Automation: "a1", Thread: 1, Action: "git push origin main", Key: "git push", Dir: "/repo", State: automation.ReviewPending, Requested: now},
		{ID: "r2", Automation: "a1", Thread: 1, Action: "npm publish", Key: "npm publish", Dir: "/repo", State: automation.ReviewPending, Requested: now},
	}
	c.syncReviewApprovals()
	c.syncReviewApprovals()
	cards := c.Approvals()
	if len(cards) != 2 || cards[0].ThreadID != "t1" || cards[0].Owner() != "miya" || cards[0].Command != "git push origin main" || cards[0].Cwd != "/repo" {
		t.Fatalf("cards: %+v", cards)
	}
	if v := c.DescribeApproval(cards[0]); v.Title == "" || v.Thread != "Deps · run" || v.Code != "git push origin main" {
		t.Fatalf("card shows %+v", v)
	}
	if c.ThreadAwaitsUser(c.ThreadWithID(1)) {
		t.Fatal("a review holds up the run that asked")
	}
	c.Answer(cards[0], interaction.Response{Action: interaction.Allow})
	if r := c.Auto.Store.Reviews[0]; r.State != automation.ReviewApproved || r.Scope != automation.ScopeOnce {
		t.Fatalf("allowed: %+v", r)
	}
	if len(c.Auto.Store.GrantsOf("a1", time.Now())) != 1 {
		t.Fatalf("grants: %+v", c.Auto.Store.Grants)
	}
	if left := c.Approvals(); len(left) != 1 || left[0].Command != "npm publish" {
		t.Fatalf("after allowing: %+v", left)
	}
	c.Answer(c.Approvals()[0], interaction.Response{Action: interaction.Decline})
	if r := c.Auto.Store.Reviews[1]; r.State != automation.ReviewDenied {
		t.Fatalf("denied: %+v", r)
	}
	if left := c.Approvals(); len(left) != 0 {
		t.Fatalf("after denying: %+v", left)
	}
	// A review decided elsewhere, on the automation's page, takes its card
	// away too.
	c.Auto.Store.Reviews = append(c.Auto.Store.Reviews, automation.Review{ID: "r3", Automation: "a1", Thread: 1, Action: "git push", Key: "git push", State: automation.ReviewPending, Requested: time.Now()})
	c.syncReviewApprovals()
	if err := c.Auto.DecideReview("r3", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if left := c.Approvals(); len(left) != 0 {
		t.Fatalf("decided on the page, still a card: %+v", left)
	}
}

// A Claude request from an external automation's run becomes a review, and
// its card stays among the approvals once the agent's own list is set.
func TestRequestSentForReviewKeepsItsCard(t *testing.T) {
	c := &Controller{}
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{{ID: 1, ServerID: "t1", Automation: "a1"}})
	c.Status, c.SetStatus = uiruntime.CreateSignal("")
	dir := t.TempDir()
	c.Auto = automation.NewRunner(automationHost{c}, filepath.Join(t.TempDir(), "automations.json"))
	c.Auto.Store.Automations = []automation.Automation{{ID: "a1", Name: "Deps", Project: dir, Instructions: "push it", Access: automation.AccessExternal, Rounds: 1}}
	c.Auto.Active["a1"] = &automation.ActiveRun{Thread: 1}
	id, _ := json.Marshal("claude:1:r1")
	ask := interaction.Request{ID: id, Method: "claude/permission", Agent: model.AgentClaude, ThreadID: "t1", Command: "Bash", Payload: json.RawMessage(`{"command":"git push origin main"}`)}
	c.setAgentApprovals(append(c.Approvals(), ask))
	cards := c.Approvals()
	if len(cards) != 1 || cards[0].Method != "miya/review" || cards[0].Command != "git push origin main" {
		t.Fatalf("approvals after a request sent for review: %+v", cards)
	}
}

// A grant for a project allows the same kind of action in an ordinary
// conversation there; another kind, another project or a local command
// still waits for the person.
func TestProjectGrantAllowsOrdinaryConversations(t *testing.T) {
	dir := t.TempDir()
	c := &Controller{Cwd: dir}
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{{ID: 2, ServerID: "t2"}})
	c.Auto = automation.NewRunner(automationHost{c}, filepath.Join(t.TempDir(), "automations.json"))
	c.Auto.Store.Grants = []automation.Grant{{Automation: "a1", Review: "r1", Scope: automation.ScopeProject, Key: "git push", Project: dir, Expires: time.Now().Add(time.Hour)}}
	claude := func(command string) interaction.Request {
		id, _ := json.Marshal("claude:2:x")
		payload, _ := json.Marshal(map[string]string{"command": command})
		return interaction.Request{ID: id, Agent: model.AgentClaude, Command: "Bash", Payload: payload}
	}
	thread := c.ThreadWithID(2)
	if !c.projectGrantAllows(claude("rtk git push origin main"), thread) {
		t.Fatal("a push in the granted project waits")
	}
	if !c.projectGrantAllows(interaction.Request{Agent: model.AgentCodex, Command: "git push"}, thread) {
		t.Fatal("a Codex push in the granted project waits")
	}
	for _, command := range []string{"gh pr create --fill", "rm -rf build", "git status"} {
		if c.projectGrantAllows(claude(command), thread) {
			t.Fatalf("%s was allowed", command)
		}
	}
	c.Cwd = t.TempDir()
	if c.projectGrantAllows(claude("git push"), thread) {
		t.Fatal("allowed in another project")
	}
}
