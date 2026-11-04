package main

import (
	"encoding/json"
	"errors"
	"strings"

	"miya/internal/codex"
	"miya/internal/desktop"
)

func (c *chatController) mcpDesktopControl(r mcpRequest) (any, error) {
	caller, ok := c.mcpCaller(r.Tool)
	if !ok {
		return nil, errors.New(mcpCallerUnknown)
	}
	reason := strings.TrimSpace(r.Task)
	if reason == "" || len(reason) > 2000 {
		return nil, errors.New("请用 1–2000 字节说明需要用户完成的操作")
	}
	for _, a := range c.approvals() {
		if a.Method == "miya/desktopControl" && a.ThreadID == caller.ServerID {
			return nil, errors.New("此会话已有待处理的人工接管请求")
		}
	}
	if len(r.WindowApp) > 256 || len(r.WindowTitle) > 1000 {
		return nil, errors.New("窗口名称过长")
	}
	hint := desktopWindowHint{App: strings.TrimSpace(r.WindowApp), Title: strings.TrimSpace(r.WindowTitle)}
	if windows, err := desktop.Windows(); err == nil {
		if target, err := chooseDesktopWindow(windows, hint); err == nil {
			hint.Window = &target
		}
	}
	id, ch := c.askUser(caller, "需要你操作授权窗口", reason+"\n在手机点窗口预览填写密码或完成授权，完成后返回此卡片确认；也可直接在 Mac 上操作。")
	list := append([]codex.Approval(nil), c.approvals()...)
	list[len(list)-1].Method = "miya/desktopControl"
	list[len(list)-1].Payload, _ = json.Marshal(hint)
	c.setApprovals(list)
	return deferred(func() (any, error) {
		if !c.waitUser(id, ch) {
			return "用户取消接管或请求已超时。请停止依赖该操作的步骤。", nil
		}
		return "用户已确认人工操作完成。请重新检查目标页面状态后继续。", nil
	}), nil
}
