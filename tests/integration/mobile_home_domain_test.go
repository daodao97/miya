//go:build !mygo_ui

package integration

import (
	"encoding/json"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/notification"
	mobile "miya/internal/phone/client"
)

func mobileHomeFixture() *mobile.Client {
	m := testPhoneClient()
	m.State.Projects[0].Threads = []model.Thread{
		{ID: 1, ServerID: "recent", Title: "整理项目文档", LastActivityAt: 500},
		{ID: 2, ServerID: "approval", Title: "更新部署配置", LastActivityAt: 100},
		{ID: 3, ServerID: "older", Title: "修复登录问题", Agent: appmodel.AgentClaude, LastActivityAt: 200},
	}
	m.State.Projects[1].Threads = []model.Thread{
		{ID: 4, ServerID: "busy", Title: "实现手机端对话列表", Busy: true, LastActivityAt: 300},
		{ID: 5, ServerID: "unread", Title: "补充接口测试", Agent: appmodel.AgentClaude, LastActivityAt: 400},
	}
	m.State.Approvals = []codex.Approval{{ID: json.RawMessage(`1`), ThreadID: "approval"}}
	m.State.Notifications.Unread = []notification.Unread{{ServerID: "unread", Agent: appmodel.AgentClaude}}
	return m
}
