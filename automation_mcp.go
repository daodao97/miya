package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/codex"
)

// The miya MCP tools for automations: an agent turns what the person said
// into settings and calls create_automation; Miya shows a confirmation card
// in that conversation and creates the automation only if they accept.

type mcpAutomationArgs struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Schedule     string `json:"schedule,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	Check        string `json:"check,omitempty"`
	Rounds       int    `json:"rounds,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
}

var automationToolProps = map[string]any{
	"name":         map[string]any{"type": "string", "description": "Short name, in the user's language"},
	"schedule":     map[string]any{"type": "string", "description": "5-field cron in local time, e.g. 0 9 * * 1-5"},
	"instructions": map[string]any{"type": "string", "description": "First message each run sends to a coding agent: what to do, when it is done, and to reply that there is nothing to report when so"},
	"check":        map[string]any{"type": "string", "description": "Optional shell command run in the project after each round; exit 0 passes, otherwise its output starts another round"},
	"rounds":       map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "description": "Most rounds when check fails (default 3; 1 without check)"},
	"agent":        map[string]any{"type": "string", "enum": []string{agentCodex, agentClaude}, "description": "Agent that runs it (default: this conversation's)"},
}

func init() {
	update := map[string]any{"id": map[string]any{"type": "string", "description": "Automation id from list_automations"},
		"enabled": map[string]any{"type": "boolean", "description": "Turn it on or off"}}
	for k, v := range automationToolProps {
		update[k] = v
	}
	mcpTools = append(mcpTools,
		mcpTool{Name: "list_automations", Description: "List the scheduled automations in Miya: id, name, project, schedule, whether on, and the last run.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Annotations: mcpReadOnly},
		mcpTool{Name: "create_automation", Description: "Create a scheduled automation in this project: on its schedule Miya starts a new conversation with the instructions. " +
			"Miya asks the user to confirm in this conversation and creates it only if they accept.",
			InputSchema: map[string]any{"type": "object", "properties": automationToolProps, "required": []string{"name", "schedule", "instructions"}},
			Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}},
		mcpTool{Name: "update_automation", Description: "Change an automation. Turning it on or off applies at once; other changes need the user's confirmation.",
			InputSchema: map[string]any{"type": "object", "properties": update, "required": []string{"id"}},
			Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}},
	)
}

// automationTool calls an automation tool in the service.
func (s *mcpServer) automationTool(name string, raw json.RawMessage) (string, error) {
	var args mcpAutomationArgs
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return "", errors.New("参数格式不对")
	}
	op := map[string]string{"list_automations": "mcpAutomationList", "create_automation": "mcpAutomationCreate", "update_automation": "mcpAutomationUpdate"}[name]
	if op == "" {
		return "", fmt.Errorf("没有工具 %s", name)
	}
	var text string
	err := s.callService(op, mcpRequest{Workspace: s.workspace, Tool: name, Auto: &args}, &text)
	return text, err
}

// mcpAutomation answers an automation tool call in the service.
func (c *chatController) mcpAutomation(op string, r mcpRequest) (any, error) {
	if c.auto == nil {
		return nil, errors.New("自动化需要 Miya 后台服务")
	}
	var args mcpAutomationArgs
	if r.Auto != nil {
		args = *r.Auto
	}
	switch op {
	case "mcpAutomationList":
		return c.automationListText(), nil
	case "mcpAutomationCreate":
		caller, ok := c.mcpCaller(r.Tool)
		if !ok {
			return nil, errors.New(mcpCallerUnknown)
		}
		a := Automation{Name: args.Name, Project: c.threadWorkspace(caller.ID), Agent: firstNonEmpty(args.Agent, threadAgent(caller)),
			Instructions: args.Instructions, Schedule: args.Schedule, Check: args.Check, Rounds: args.Rounds, Enabled: true}
		if a.Rounds == 0 {
			a.Rounds = 3
		}
		if err := a.normalize(); err != nil {
			return nil, err
		}
		a.ID = ""
		id, ch := c.askUser(caller, "创建自动化「"+a.Name+"」", automationCardDetail(a, c.auto.now()))
		return deferred(func() (any, error) {
			if !c.waitUser(id, ch) {
				return "用户没有确认，自动化未创建。", nil
			}
			v, err := c.onMain(func() (any, error) {
				saved, err := c.saveAutomation(a)
				if err != nil {
					return nil, err
				}
				return automationViewOf(saved, nil, false, c.auto.now()), nil
			})
			if err != nil {
				return nil, err
			}
			view := v.(automationView)
			return fmt.Sprintf("已创建自动化「%s」（id %s），%s，下次运行 %s。", view.Name, view.ID, view.ScheduleText, formatAutomationTime(view.Next)), nil
		}), nil
	case "mcpAutomationUpdate":
		return c.mcpUpdateAutomation(r, args)
	}
	return nil, fmt.Errorf("unknown miya call %s", op)
}

func (c *chatController) mcpUpdateAutomation(r mcpRequest, args mcpAutomationArgs) (any, error) {
	s := c.auto
	i, ok := s.store.find(strings.TrimSpace(args.ID))
	if !ok {
		for j, a := range s.store.Automations {
			if a.Name == strings.TrimSpace(args.ID) {
				i, ok = j, true
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("没有自动化 %q：用 list_automations 查看", args.ID)
	}
	old := s.store.Automations[i]
	next := old
	if args.Name != "" {
		next.Name = args.Name
	}
	if args.Schedule != "" {
		next.Schedule = args.Schedule
	}
	if args.Instructions != "" {
		next.Instructions = args.Instructions
	}
	if args.Check != "" {
		next.Check = args.Check
	}
	if args.Rounds != 0 {
		next.Rounds = args.Rounds
	}
	if args.Agent != "" {
		next.Agent = args.Agent
	}
	if args.Enabled != nil {
		next.Enabled = *args.Enabled
	}
	if err := next.normalize(); err != nil {
		return nil, err
	}
	diff := automationDiff(old, next)
	if diff == "" {
		if next.Enabled == old.Enabled {
			return "没有需要修改的内容。", nil
		}
		// Turning one on or off needs no card: the person asked for it.
		if _, err := c.automationOp("automationToggle", automationRequest{ID: old.ID, Enabled: next.Enabled}); err != nil {
			return nil, err
		}
		if next.Enabled {
			return "已启用「" + old.Name + "」。", nil
		}
		return "已暂停「" + old.Name + "」。", nil
	}
	caller, ok := c.mcpCaller(r.Tool)
	if !ok {
		return nil, errors.New(mcpCallerUnknown)
	}
	id, ch := c.askUser(caller, "修改自动化「"+old.Name+"」", diff)
	return deferred(func() (any, error) {
		if !c.waitUser(id, ch) {
			return "用户没有确认，自动化未修改。", nil
		}
		if _, err := c.onMain(func() (any, error) { return c.saveAutomation(next) }); err != nil {
			return nil, err
		}
		return "已修改「" + next.Name + "」，" + describeCron(next.Schedule) + "。", nil
	}), nil
}

func (c *chatController) automationListText() string {
	s := c.auto
	if len(s.store.Automations) == 0 {
		return "还没有自动化。"
	}
	var b strings.Builder
	for _, a := range s.store.Automations {
		state := "已暂停"
		if a.Enabled {
			state = "已启用，下次 " + formatAutomationTime(a.Next(s.now()))
		}
		fmt.Fprintf(&b, "%s「%s」· %s · %s · %s", a.ID, a.Name, filepath.Base(a.Project), describeCron(a.Schedule), state)
		if r, ok := s.store.lastRun(a.ID); ok {
			fmt.Fprintf(&b, " · 上次%s", runResultLabel(r.Result))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

var weekdayNames = []string{"日", "一", "二", "三", "四", "五", "六"}

func formatAutomationTime(t time.Time) string {
	if t.IsZero() {
		return "未安排"
	}
	return fmt.Sprintf("%d月%d日 周%s %s", t.Month(), t.Day(), weekdayNames[t.Weekday()], t.Format("15:04"))
}

func automationCheckText(a Automation) string {
	if a.Check == "" {
		return "不检查，跑 1 轮"
	}
	return fmt.Sprintf("%s 通过，最多 %d 轮", a.Check, a.Rounds)
}

// automationCardDetail is what the confirmation card shows of a new one.
func automationCardDetail(a Automation, now time.Time) string {
	return strings.Join([]string{
		fmt.Sprintf("时间：%s（%s），下次 %s", describeCron(a.Schedule), a.Schedule, formatAutomationTime(a.Next(now))),
		"项目：" + a.Project,
		"Agent：" + backendFor(a.Agent).Name(),
		"完成标准：" + automationCheckText(a),
		"指令：" + a.Instructions,
	}, "\n")
}

// automationDiff lists what a change changes, besides turning it on or off.
func automationDiff(old, next Automation) string {
	var lines []string
	add := func(label, from, to string) {
		if from != to {
			lines = append(lines, label+"："+from+" → "+to)
		}
	}
	add("名称", old.Name, next.Name)
	add("时间", describeCron(old.Schedule), describeCron(next.Schedule))
	add("Agent", backendFor(old.Agent).Name(), backendFor(next.Agent).Name())
	add("完成标准", automationCheckText(old), automationCheckText(next))
	add("指令", old.Instructions, next.Instructions)
	return strings.Join(lines, "\n")
}

// Miya's own requests to the person, shown as approval cards in a
// conversation: their ids start with miya: and Miya answers them itself.

func isMiyaApproval(a codex.Approval) bool { return strings.Contains(string(a.ID), "\"miya:") }

// askUser shows a card in caller's conversation and returns its id and the
// channel its answer arrives on.
func (c *chatController) askUser(caller Thread, reason, detail string) (json.RawMessage, chan bool) {
	c.miyaSerial++
	id, _ := json.Marshal(fmt.Sprintf("miya:%d:%d", caller.ID, c.miyaSerial))
	ch := make(chan bool, 1)
	if c.miyaPending == nil {
		c.miyaPending = map[string]chan bool{}
	}
	c.miyaPending[string(id)] = ch
	a := codex.Approval{ID: id, Method: "miya/automation", ThreadID: caller.ServerID, Reason: reason, Command: detail}
	c.setApprovals(append(append([]codex.Approval(nil), c.approvals()...), a))
	return id, ch
}

func (c *chatController) respondMiya(a codex.Approval, result any) {
	c.removeRequest(a.ID)
	ch := c.miyaPending[string(a.ID)]
	delete(c.miyaPending, string(a.ID))
	if ch == nil {
		return
	}
	data, _ := json.Marshal(result)
	var r struct {
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(data, &r)
	ch <- r.Decision == "accept"
}

// miyaRequestTimeout is how long a card waits; then it counts as declined.
const miyaRequestTimeout = 10 * time.Minute

// waitUser waits off the main thread for the answer to a card.
func (c *chatController) waitUser(id json.RawMessage, ch chan bool) bool {
	select {
	case ok := <-ch:
		return ok
	case <-time.After(miyaRequestTimeout):
		c.dispatch(func() {
			delete(c.miyaPending, string(id))
			c.removeRequest(id)
		})
		return false
	}
}

// onMain runs f on the main thread and waits for it.
func (c *chatController) onMain(f func() (any, error)) (any, error) {
	type result struct {
		v   any
		err error
	}
	done := make(chan result, 1)
	c.dispatch(func() {
		v, err := f()
		done <- result{v, err}
	})
	select {
	case r := <-done:
		return r.v, r.err
	case <-time.After(30 * time.Second):
		return nil, errors.New("Miya 没有响应")
	}
}
