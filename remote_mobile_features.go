package main

import (
	"errors"
	"fmt"
	"slices"

	"miya/internal/remote"
)

type phoneModelOption struct {
	Value         string   `json:"value"`
	Label         string   `json:"label"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort"`
}

type phoneAgentOptions struct {
	Agent   string             `json:"agent"`
	Current remote.SendOptions `json:"current"`
	Models  []phoneModelOption `json:"models"`
}

func (c *chatController) phoneAgentOptions() []phoneAgentOptions {
	var out []phoneAgentOptions
	for _, b := range agentBackends {
		a := phoneAgentOptions{Agent: b.ID(), Current: remote.SendOptions{Model: b.Model(c), Effort: b.Effort(c), Policy: c.selectedPermissionPolicy()}}
		var choices []string
		if b.ID() == agentCodex && c.models == nil {
			if a.Current.Model != "" {
				choices = []string{a.Current.Model}
			}
		} else {
			choices = b.ModelChoices(c)
		}
		if !slices.Contains(choices, "") {
			choices = append([]string{""}, choices...)
		}
		for _, value := range choices {
			m := phoneModelOption{Value: value, Label: b.ModelLabel(c, value)}
			if b.ID() == agentClaude {
				m.Efforts = b.EffortChoices(c)
			} else if c.models != nil {
				for _, model := range c.models() {
					if model.Model == value || value == "" && model.IsDefault {
						m.DefaultEffort = model.DefaultReasoningEffort
						for _, e := range model.SupportedReasoningEfforts {
							m.Efforts = append(m.Efforts, e.ReasoningEffort)
						}
						break
					}
				}
			}
			if len(m.Efforts) == 0 && value == a.Current.Model && a.Current.Effort != "" {
				m.Efforts = []string{a.Current.Effort}
				m.DefaultEffort = a.Current.Effort
			}
			a.Models = append(a.Models, m)
		}
		out = append(out, a)
	}
	return out
}

func (c *chatController) phoneSendRequest(r remote.Request, paths []string, agent string) (sendRequest, error) {
	if agent == "" {
		agent = agentCodex
	}
	if agent != agentCodex && agent != agentClaude {
		return sendRequest{}, errors.New("不支持的 Agent")
	}
	req := sendRequest{Agent: agent, Text: r.Text, Attachments: paths, Steer: r.Steer}
	if r.Options == nil {
		return req, nil
	}
	for _, a := range c.phoneAgentOptions() {
		if a.Agent != agent {
			continue
		}
		var model *phoneModelOption
		for i := range a.Models {
			if a.Models[i].Value == r.Options.Model {
				model = &a.Models[i]
				break
			}
		}
		if model == nil {
			return req, fmt.Errorf("模型 %s 当前不可用，请重新选择", r.Options.Model)
		}
		if r.Options.Effort != "" && !slices.Contains(model.Efforts, r.Options.Effort) {
			return req, errors.New("此模型不支持所选推理强度，请重新选择")
		}
		switch r.Options.Policy {
		case "", "on-request", "on-failure", "never":
		default:
			return req, errors.New("无效的权限模式")
		}
		req.Model, req.Effort, req.Policy = r.Options.Model, r.Options.Effort, r.Options.Policy
		req.ExplicitSettings = true
		return req, nil
	}
	return req, errors.New("Agent 设置尚未就绪")
}

func (c *chatController) phoneQueueAction(r remote.Request) error {
	target := c.phoneThreadFor(r)
	if !target.found {
		return errors.New("对话已不在桌面端")
	}
	if target.cwd != c.cwd {
		return errors.New("请先在此项目发送消息，再管理队列")
	}
	switch r.Op {
	case "dequeue":
		c.removeQueued(target.thread.ID, r.QueuedID)
	case "steer-queued":
		c.steerQueued(target.thread.ID, r.QueuedID)
	case "drain-queue":
		c.resumeQueue(target.thread.ID)
	default:
		return errors.New("未知队列操作")
	}
	return nil
}
