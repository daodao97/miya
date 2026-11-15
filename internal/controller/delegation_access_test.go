package controller

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/chat/model"
	"miya/internal/delegation/coordinator"
	"miya/internal/interaction"
)

// A delegated task's access level only narrows what the delegating
// conversation may do, and applies to a new conversation.
func TestDelegationAccessOnlyNarrows(t *testing.T) {
	c := &Controller{}
	manual := model.Thread{ID: 1, PermissionPolicy: "on-request"}
	open := model.Thread{ID: 2, PermissionPolicy: "never"}
	cases := []struct {
		caller model.Thread
		args   coordinator.Args
		want   string
	}{
		{manual, coordinator.Args{}, ""},
		{manual, coordinator.Args{Access: "read"}, ""},
		{manual, coordinator.Args{Access: "edit"}, "ACCESS_DENIED"},
		{open, coordinator.Args{Access: "edit"}, ""},
		{model.Thread{ID: 3, PermissionPolicy: "edit"}, coordinator.Args{Access: "edit"}, ""},
		{open, coordinator.Args{Access: "external"}, "INVALID_ARGUMENT"},
		{open, coordinator.Args{Access: "read", Thread: "miya://x/thread/codex/y"}, "INVALID_ARGUMENT"},
	}
	for _, tc := range cases {
		err := c.checkDelegationAccess(tc.caller, tc.args)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)) {
			t.Errorf("%s from %s: %v, want %q", tc.args.Access, tc.caller.PermissionPolicy, err, tc.want)
		}
	}
}

// Miya answers the requests of a running delegated task with an access
// level: local work its level allows goes on, the rest is refused, and a
// conversation with no running task waits for the person as before.
func TestDelegatedTaskRequestsAreAnswered(t *testing.T) {
	dir := t.TempDir()
	c := &Controller{Cwd: dir}
	c.Collaboration = &coordinator.Coordinator{Executions: map[string]*coordinator.Execution{"task-e": {Thread: 3}, "task-r": {Thread: 4}}}
	edit := model.Thread{ID: 3, PermissionPolicy: "edit"}
	read := model.Thread{ID: 4, PermissionPolicy: "read"}
	idle := model.Thread{ID: 5, PermissionPolicy: "edit"}
	claude := func(tool string, input map[string]string) interaction.Request {
		id, _ := json.Marshal("claude:3:x")
		payload, _ := json.Marshal(input)
		return interaction.Request{ID: id, Agent: model.AgentClaude, Command: tool, Payload: payload}
	}
	bash := func(command string) interaction.Request { return claude("Bash", map[string]string{"command": command}) }
	cases := []struct {
		name   string
		thread model.Thread
		req    interaction.Request
		allow  bool
		says   string
	}{
		{"edit writes in the workspace", edit, claude("Write", map[string]string{"file_path": filepath.Join(dir, "a.go")}), true, ""},
		{"edit runs tests", edit, bash("go test ./..."), true, ""},
		{"edit pushes", edit, bash("rtk git push origin main"), false, "outside the workspace"},
		{"edit writes elsewhere", edit, claude("Write", map[string]string{"file_path": "/etc/hosts"}), false, "outside the workspace"},
		{"edit Codex request", edit, interaction.Request{Agent: model.AgentCodex, Command: "curl https://x"}, false, "outside the workspace"},
		{"edit commits and pushes in one go", edit, bash("echo x >> README.md && git commit -am x && git push origin HEAD:main"), false, "git push origin HEAD:main (nothing else in that command ran)"},
		{"read reads", read, bash("git log --oneline -3"), true, ""},
		{"read writes", read, claude("Write", map[string]string{"file_path": filepath.Join(dir, "a.go")}), false, "read only"},
		{"read runs a command", read, bash("go test ./..."), false, "read only"},
		{"miya tools", read, claude("mcp__miya__report_result", nil), true, ""},
	}
	for _, tc := range cases {
		got := c.delegatedAnswer(tc.req, tc.thread)
		if !got.Decided || got.Allow != tc.allow || !strings.Contains(got.Message, tc.says) {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
	if got := c.delegatedAnswer(bash("git push"), idle); got.Decided {
		t.Fatalf("a conversation with no running task was answered: %+v", got)
	}
	if got := c.delegatedAnswer(bash("git push"), model.Thread{ID: 3}); got.Decided {
		t.Fatalf("a task without an access level was answered: %+v", got)
	}
}
