package main

import (
	"encoding/json"
	"fmt"
	"miya/internal/codex"
	"strings"
	"testing"
)

func testController() *chatController {
	c := &chatController{}
	c.threads, c.setThreads = createSignal([]Thread{{ID: 1, ServerID: "t1", Busy: true}, {ID: 2, ServerID: "t2"}})
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})
	return c
}
func event(method, params string) codex.Frame {
	return codex.Frame{Method: method, Params: json.RawMessage(params)}
}
func TestStreamReconciliationAndRouting(t *testing.T) {
	c := testController()
	c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
	c.handle(event("item/agentMessage/delta", `{"threadId":"t1","turnId":"turn1","itemId":"a","delta":"Hello "}`))
	c.handle(event("item/agentMessage/delta", `{"threadId":"t1","turnId":"turn1","itemId":"a","delta":"世界"}`))
	c.handle(event("item/completed", `{"threadId":"t1","turnId":"turn1","item":{"id":"a","type":"agentMessage","text":"Hello 世界!"}}`))
	first := c.threads()[0]
	if len(first.Messages) != 1 || first.Messages[0].Content != "Hello 世界!" {
		t.Fatalf("bad final reconciliation: %+v", first.Messages)
	}
	if len(c.threads()[1].Messages) != 0 {
		t.Fatal("event leaked to other thread")
	}
	c.handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"old","status":"completed"}}`))
	if !c.threads()[0].Busy {
		t.Fatal("stale completion stopped active turn")
	}
	c.handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"interrupted"}}`))
	if c.threads()[0].Busy || c.threads()[0].Status != "Stopped" {
		t.Fatal("interrupt not reflected")
	}
}

func TestThreadTitleUsesServerName(t *testing.T) {
	c := testController()
	c.handle(event("thread/name/updated", `{"threadId":"t1","name":"按 service 分组统计"}`))
	if got := c.threads()[0].Title; got != "按 service 分组统计" {
		t.Fatalf("server title not applied: %q", got)
	}
}
func TestOptimisticUserIsNotDuplicated(t *testing.T) {
	c := testController()
	c.update(1, func(t *Thread) { t.Messages = []Message{{ID: 9, Role: "user", Content: "Hi", ItemID: "pending-user"}} })
	e := event("item/started", `{"threadId":"t1","turnId":"turn1","item":{"id":"u","type":"userMessage","content":[{"type":"text","text":"Hi"}]}}`)
	c.handle(e)
	e.Method = "item/completed"
	c.handle(e)
	if len(c.threads()[0].Messages) != 1 {
		t.Fatal("duplicate user message")
	}
}

func TestOptimisticUserIsNotDuplicatedAcrossStartedAndCompleted(t *testing.T) {
	c := testController()
	c.update(1, func(t *Thread) { t.Messages = []Message{{ID: 9, Role: "user", Content: "Hi", ItemID: "pending-user"}} })
	params := `{"threadId":"t1","turnId":"turn1","item":{"id":"u","type":"userMessage","content":[{"type":"text","text":"Hi"}]}}`
	c.handle(event("item/started", params))
	c.handle(event("item/completed", params))
	if len(c.threads()[0].Messages) != 1 || c.threads()[0].Messages[0].ItemID != "u" {
		t.Fatalf("started/completed duplicated optimistic user: %+v", c.threads()[0].Messages)
	}
}
func TestToolOutputBounded(t *testing.T) {
	m, ok := itemMessage(codex.Item{Type: "commandExecution", Command: "test", AggregatedOutput: strings.Repeat("a", 10000)})
	if !ok || len(m.Content) > 8100 || !strings.Contains(m.Content, "omitted") {
		t.Fatal("unbounded tool output")
	}

}

func TestAttachmentInputs(t *testing.T) {
	c := testController()
	inputs := c.attachmentInputs([]string{"/tmp/photo.png", "/tmp/notes.md"})
	if len(inputs) != 2 || inputs[0]["type"] != "localImage" || inputs[1]["type"] != "text" {
		t.Fatalf("unexpected attachment inputs: %#v", inputs)
	}
}

func TestPermissionPolicySandboxMapping(t *testing.T) {
	for _, tc := range []struct {
		policy, sandbox string
	}{
		{"on-request", "workspace-write"},
		{"on-failure", "workspace-write"},
		{"never", "danger-full-access"},
	} {
		if got := sandboxForPermissionPolicy(tc.policy); got != tc.sandbox {
			t.Fatalf("%s sandbox = %s, want %s", tc.policy, got, tc.sandbox)
		}
	}
	c := testController()
	if got := c.selectedPermissionPolicy(); got != "on-request" {
		t.Fatalf("default policy = %s", got)
	}
}

func TestNormalizeLocale(t *testing.T) {
	tests := map[string]string{
		"zh": "zh", "zh-CN": "zh", "中文": "zh",
		"en": "en", "en-US": "en", "English": "en", "": "en", "fr": "en",
	}
	for input, want := range tests {
		if got := normalizeLocale(input); got != want {
			t.Fatalf("normalizeLocale(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAllProtocolItemTypes(t *testing.T) {
	fixtures := []struct{ kind, raw, role, contains string }{
		{"userMessage", `{"content":[{"type":"text","text":"hello"},{"type":"localImage","path":"/tmp/image.png"}]}`, "user", "image.png"},
		{"agentMessage", `{"text":"answer"}`, "assistant", "answer"},
		{"reasoning", `{"summary":["checking"],"content":["private detail"]}`, "activity", "checking"},
		{"plan", `{"text":"step one"}`, "activity", "step one"},
		{"commandExecution", `{"status":"failed","command":"false","exitCode":1,"aggregatedOutput":"stderr"}`, "tool", "Exit code: 1"},
		{"fileChange", `{"status":"completed","changes":[{"path":"a.go","diff":"+new"}]}`, "tool", "+new"},
		{"mcpToolCall", `{"status":"completed","server":"local","tool":"lookup","result":{"answer":42}}`, "tool", "42"},
		{"dynamicToolCall", `{"status":"failed","tool":"custom","error":{"message":"not available"}}`, "tool", "not available"},
		{"collabAgentToolCall", `{"tool":"spawnAgent","status":"completed","agentsStates":{"child":{"status":"done"}}}`, "tool", ""},
		{"subAgentActivity", `{}`, "activity", "activity"},
		{"webSearch", `{"query":"test"}`, "tool", "test"},
		{"imageView", `{"path":"/tmp/test.png"}`, "tool", "test.png"},
		{"imageGeneration", `{"status":"completed","savedPath":"/tmp/generated.png"}`, "tool", "generated.png"},
		{"sleep", `{"durationMs":100}`, "activity", "100"},
		{"enteredReviewMode", `{"review":"review started"}`, "activity", "review started"},
		{"exitedReviewMode", `{"review":"review done"}`, "activity", "review done"},
		{"contextCompaction", `{}`, "activity", "compacted"},
		{"functionCallOutput", `{"output":"tool returned"}`, "tool", "tool returned"},
		{"hookPrompt", `{}`, "activity", "Hook"},
		{"futureItem", `{}`, "activity", "futureItem"},
	}
	for _, f := range fixtures {
		t.Run(f.kind, func(t *testing.T) {
			raw := `{"type":"` + f.kind + `"}`
			if f.raw != "{}" {
				raw = `{"type":"` + f.kind + `",` + f.raw[1:]
			}
			var item codex.Item
			if err := json.Unmarshal([]byte(raw), &item); err != nil {
				t.Fatal(err)
			}
			m, ok := itemMessage(item)
			if !ok || m.Role != f.role || !strings.Contains(m.Content, f.contains) {
				t.Fatalf("bad mapping: %+v", m)
			}
			// Collab agent states moved from the text to the agents list.
			if f.kind == "collabAgentToolCall" && (len(m.Agents) != 1 || m.Agents[0].ID != "child" || m.Agents[0].Status != subAgentCompleted) {
				t.Fatalf("collab agents lost: %+v", m.Agents)
			}
			if strings.Contains(m.Content, "private detail") {
				t.Fatal("raw reasoning was exposed")
			}
		})
	}
}
func TestProgressErrorsAndRequestResolution(t *testing.T) {
	c := testController()
	for _, method := range []string{"item/plan/delta", "item/reasoning/summaryTextDelta", "item/commandExecution/outputDelta", "item/fileChange/outputDelta"} {
		c.handle(event(method, `{"threadId":"t1","turnId":"turn1","itemId":"`+method+`","delta":"progress"}`))
	}
	if len(c.threads()[0].Messages) != 4 {
		t.Fatal("progress notifications dropped")
	}
	c.handle(event("turn/plan/updated", `{"threadId":"t1","turnId":"turn1","plan":[{"step":"verify","status":"in_progress"}]}`))
	c.handle(event("turn/plan/updated", `{"threadId":"t1","turnId":"turn1","plan":[{"step":"verify","status":"completed"}]}`))
	if len(c.threads()[0].Messages) != 5 || !strings.Contains(c.threads()[0].Messages[4].Content, "completed") {
		t.Fatal("plan not updated in place")
	}
	c.handle(event("turn/diff/updated", `{"threadId":"t1","turnId":"turn1","diff":"+line"}`))
	c.handle(event("thread/tokenUsage/updated", `{"threadId":"t1","tokenUsage":{"last":{"totalTokens":123}}}`))
	if c.threads()[0].Tokens != 123 {
		t.Fatal("usage missing")
	}
	c.handle(event("error", `{"threadId":"t1","willRetry":true,"error":{"message":"temporary"}}`))
	if c.threads()[0].Status != "Retrying: temporary" || !c.threads()[0].Busy {
		t.Fatal("retry state wrong")
	}
	c.setApprovals([]codex.Approval{{ID: json.RawMessage(`7`), ThreadID: "t1", TurnID: "turn1"}, {ID: json.RawMessage(`"other"`), ThreadID: "t2", TurnID: "turn2"}})
	c.handle(event("serverRequest/resolved", `{"threadId":"t1","requestId":7}`))
	if len(c.approvals()) != 1 || c.approvals()[0].ThreadID != "t2" {
		t.Fatal("resolved request cleanup wrong")
	}
	c.handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"failed","error":{"message":"failure"}}}`))
	if c.threads()[0].Busy || c.threads()[0].Status != "failure" {
		t.Fatal("failed turn state wrong")
	}
}
func TestApprovalDecisionsAndForms(t *testing.T) {
	for _, method := range []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval"} {
		for _, allow := range []bool{false, true} {
			a := codex.Approval{Method: method, Permissions: json.RawMessage(`{"network":{"enabled":true}}`)}
			b, _ := json.Marshal(approvalResult(a, allow))
			if method == "item/permissions/requestApproval" {
				if allow && !strings.Contains(string(b), `"enabled":true`) {
					t.Fatal(string(b))
				}
				if !allow && strings.Contains(string(b), "enabled") {
					t.Fatal("denial granted permissions")
				}
			} else {
				decision := "decline"
				if allow {
					decision = "accept"
				}
				if !strings.Contains(string(b), decision) {
					t.Fatal(string(b))
				}
			}
		}
	}
	schema := json.RawMessage(`{"type":"object","required":["name"],"properties":{"name":{"type":"string","enum":["yes","no"]},"count":{"type":"integer"}}}`)
	for _, content := range []map[string]any{{}, {"name": true}, {"name": "invalid"}, {"name": "yes", "count": 1.5}, {"name": "yes", "extra": "oops"}} {
		if validateForm(schema, content) == nil {
			t.Fatalf("invalid form accepted: %v", content)
		}
	}
	if err := validateForm(schema, map[string]any{"name": "yes", "count": float64(2)}); err != nil {
		t.Fatal(err)
	}
}

func TestServerRequestQueueAndInterruptedCleanup(t *testing.T) {
	c := testController()
	for i, method := range []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request"} {
		f := event(method, `{"threadId":"t1","turnId":"turn1","itemId":"item"}`)
		f.ID = json.RawMessage(fmt.Sprintf("%d", i))
		c.serverRequest(f)
		c.serverRequest(f)
	}
	if len(c.approvals()) != 5 {
		t.Fatalf("requests missing or duplicated: %d", len(c.approvals()))
	}
	c.handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"interrupted"}}`))
	if len(c.approvals()) != 0 {
		t.Fatal("interrupted turn kept stale requests")
	}
}

func TestUnsupportedMCPConstraintsCannotBeSilentlyAccepted(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"label":{"type":"string","minLength":5}}}`)
	if err := validateForm(raw, map[string]any{"label": "x"}); err == nil {
		t.Fatal("unsupported constraint was silently ignored")
	}
}

func TestPermissionModes(t *testing.T) {
	for _, tc := range []struct{ mode, approval, reviewer, sandbox string }{
		{"on-request", "on-request", "user", "workspaceWrite"},
		{"on-failure", "on-request", "auto_review", "workspaceWrite"},
		{"never", "never", "user", "dangerFullAccess"},
	} {
		if approvalForPermissionPolicy(tc.mode) != tc.approval || reviewerForPermissionPolicy(tc.mode) != tc.reviewer || turnSandboxForPermissionPolicy(tc.mode)["type"] != tc.sandbox {
			t.Fatalf("incorrect permission routing for %s", tc.mode)
		}
	}
	if turnSandboxForPermissionPolicy("on-failure")["networkAccess"] != false {
		t.Fatal("auto review must retain the network sandbox")
	}
}

func TestProjectOrderUsesLatestConversation(t *testing.T) {
	c := testController()
	c.cwd = "/workspace/current"
	c.projects, c.setProjects = createSignal([]Project{
		{Name: "old", Path: "/workspace/old"},
		{Name: "current", Path: c.cwd},
		{Name: "new", Path: "/workspace/new"},
	})
	c.projectCache = map[string][]Thread{
		"/workspace/old":     {{LastActivityAt: 100}},
		"/workspace/current": {{LastActivityAt: 200}},
		"/workspace/new":     {{LastActivityAt: 300}},
	}
	c.refreshProjectOrder()
	got := c.projects()
	if got[0].Name != "new" || got[1].Name != "current" || got[2].Name != "old" {
		t.Fatalf("project order = %#v", got)
	}
}

func TestResumeWithoutClientDoesNotPanic(t *testing.T) {
	c := testController()
	c.resume(Thread{ID: 1, ServerID: "thread-1"}, nil)
	if c.threads()[0].Status != "Codex is reconnecting…" {
		t.Fatalf("status = %q", c.threads()[0].Status)
	}
}

func TestConversationPermissionOverride(t *testing.T) {
	c := testController()
	c.active, c.setActive = createSignal(1)
	c.permissionPolicy, c.setPermissionPolicy = createSignal("on-request")
	c.draftPermission, c.setDraftPermission = createSignal("")
	c.setCurrentPermissionPolicy("on-failure")
	if c.currentPermissionPolicy() != "on-failure" || c.selectedPermissionPolicy() != "on-request" {
		t.Fatal("conversation override changed the default")
	}
	c.setActive(2)
	if c.currentPermissionPolicy() != "on-request" {
		t.Fatal("permission leaked to another thread")
	}
	c.setActive(1)
	if c.currentPermissionPolicy() != "on-failure" {
		t.Fatal("override lost on thread switch")
	}
	c.setActive(0)
	c.setCurrentPermissionPolicy("never")
	if c.currentPermissionPolicy() != "never" || c.threads()[0].PermissionPolicy != "on-failure" {
		t.Fatal("new conversation permission changed an existing thread")
	}
}

func TestBackgroundThreadsContinueAfterSelection(t *testing.T) {
	c := testController()
	c.active, c.setActive = createSignal(1)
	c.draft, c.setDraft = createSignal("")
	c.attachments, c.setAttachments = createSignal([]string{})
	c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"a"}}`))
	c.selectThread(c.threads()[1])
	c.handle(event("turn/started", `{"threadId":"t2","turn":{"id":"b"}}`))
	if !c.threads()[0].Busy || !c.threads()[1].Busy {
		t.Fatal("switching stopped a running thread")
	}
	c.handle(event("item/agentMessage/delta", `{"threadId":"t1","turnId":"a","itemId":"answer","delta":"background reply"}`))
	if c.active() != 2 || c.threads()[0].Messages[0].Content != "background reply" {
		t.Fatal("background stream changed selection or lost output")
	}
	c.handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"a","status":"completed"}}`))
	if c.threads()[0].Busy || !c.threads()[1].Busy {
		t.Fatal("completion affected another thread")
	}
	c.selectThread(c.threads()[0])
	if !c.threads()[1].Busy {
		t.Fatal("switching back stopped the second thread")
	}
}
