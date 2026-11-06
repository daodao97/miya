package integration

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	claude "miya/internal/agent/claude/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	projectmodel "miya/internal/project/model"
)

func TestMobileClaudeQuestionRoutesFromControllerToForm(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			c := testController()
			c.Cwd = "/a"
			// The question belongs to a background project, not the active one.
			th := model.Thread{ID: 3, Agent: appmodel.AgentClaude, ServerID: "claude-session", Title: "解锁设置", Busy: true, HistoryLoaded: true}
			c.SetProjects([]projectmodel.Project{{Name: "proj-b", Path: "/b"}})
			c.ProjectCache = map[string][]model.Thread{"/b": {th}}
			var f claude.Frame
			if err := json.Unmarshal([]byte(`{"type":"control_request","request_id":"question-1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"tool-1","input":{"questions":[{"question":"自动填入密码前是否验证？","header":"验证方式","multiSelect":false,"options":[{"label":"每次 Face ID","description":"取用密码时验证身份。"},{"label":"不验证，全自动","description":"手机解锁后直接取用密码。"}]},{"question":"何时执行解锁？","header":"触发时机","multiSelect":false,"options":[{"label":"连接后自动解锁"},{"label":"显示解锁按钮"}]}]}}}`), &f); err != nil {
				t.Fatal(err)
			}
			c.HandleClaude(th.ID, f)
			// Exercise the actual wire format rather than constructing a phone approval.
			data, err := json.Marshal(c.PhoneSnapshot())
			if err != nil {
				t.Fatal(err)
			}
			m := testMobileApp()
			var state phoneproto.State
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			m.ApplyPhoneState(state)
			if len(m.ApprovalsFor(th.ServerID)) != 1 || len(m.ApprovalsFor("t1")) != 0 {
				t.Fatal("Claude question is missing from its conversation or leaked to another")
			}
			m.OpenThread(th)
			m.ShowThread(th)
			tt := ui.NewTester(m.ViewRoot, width, 750)
			tt.Frame()
			r, ok := tt.Find("Enter your answer")
			if !ok || r.Y < 0 || r.Y+r.H > 750 {
				t.Fatalf("Claude question entry is missing or off-screen: %v", r)
			}
			if err := tt.Click("Enter your answer"); err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{"Claude Code", "验证方式", "触发时机", "取用密码时验证身份。"} {
				if !tt.HasText(label) {
					t.Fatalf("question form lost %q", label)
				}
			}
			for _, label := range []string{"每次 Face ID", "显示解锁按钮"} {
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			a := m.State.Approvals[0]
			if m.FieldsFor(a)["自动填入密码前是否验证？"] != "每次 Face ID" || m.FieldsFor(a)["何时执行解锁？"] != "显示解锁按钮" {
				t.Fatal("Claude question options did not retain separate answers")
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("claude-questions-%d", width))
			if err := tt.Click("Submit answer"); err != nil {
				t.Fatal(err)
			}
			if m.FormErrors[string(a.ID)] != "Not connected to desktop" || len(m.State.Approvals) != 1 {
				t.Fatal("failed submission lost the question or its answers")
			}
			f.Type = "control_cancel_request"
			c.HandleClaude(th.ID, f)
			if len(c.Approvals()) != 0 {
				t.Fatal("cancelled Claude question remained pending")
			}
		})
	}
}
