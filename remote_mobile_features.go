package main

import (
	"errors"
	"fmt"
	"math"
	"miya/internal/remote"
	"slices"
	"strings"
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

func (c *chatController) phoneSendRequest(r remote.Request, paths []string, agent string) (SendRequest, error) {
	if agent == "" {
		agent = agentCodex
	}
	if agent != agentCodex && agent != agentClaude {
		return SendRequest{}, errors.New("Unsupported agent")
	}
	req := SendRequest{Agent: agent, Text: r.Text, Attachments: paths, Steer: r.Steer}
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
			return req, fmt.Errorf("Model %s is unavailable. Select another model.", r.Options.Model)
		}
		if r.Options.Effort != "" && !slices.Contains(model.Efforts, r.Options.Effort) {
			return req, errors.New("This model does not support the selected reasoning effort. Select another level.")
		}
		switch r.Options.Policy {
		case "", "on-request", "on-failure", "never":
		default:
			return req, errors.New("Invalid permission mode")
		}
		req.Model, req.Effort, req.Policy = r.Options.Model, r.Options.Effort, r.Options.Policy
		req.ExplicitSettings = true
		return req, nil
	}
	return req, errors.New("Agent settings are not ready")
}

func (c *chatController) phoneQueueAction(r remote.Request) error {
	target := c.phoneThreadFor(r)
	if !target.found {
		return errors.New("Conversation is no longer on the desktop")
	}
	if target.cwd != c.cwd {
		return errors.New("Send a message in this project before managing the queue")
	}
	switch r.Op {
	case "dequeue":
		c.removeQueued(target.thread.ID, r.QueuedID)
	case "steer-queued":
		c.steerQueued(target.thread.ID, r.QueuedID)
	case "drain-queue":
		c.resumeQueue(target.thread.ID)
	default:
		return errors.New("Unknown queue action")
	}
	return nil
}

// Only subscription usage is shared, never account identity or credentials.
// Agents without real quota data are omitted entirely.
type phoneAgentQuota struct {
	Agent   string             `json:"agent"`
	Windows []phoneQuotaWindow `json:"windows"`
}

type phoneQuotaWindow struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    int64   `json:"resetsAt,omitempty"`
}

func (c *chatController) phoneQuotas() []phoneAgentQuota {
	q := c.quotaLimits.RateLimits
	if v, ok := c.quotaLimits.ByID[codexAccountLimitID]; ok {
		q = &v
	}
	if q == nil {
		return nil
	}
	windows := composerQuotaWindows(c.quotaLimits)
	// Preserve additional account windows without using model-specific buckets.
	for _, w := range []*quotaWindow{q.Primary, q.Secondary} {
		if w == nil {
			continue
		}
		found := false
		for _, existing := range windows {
			if existing == w {
				found = true
			}
		}
		if !found {
			windows = append(windows, w)
		}
	}
	quota := phoneAgentQuota{Agent: agentCodex}
	for i, w := range windows {
		if math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) {
			continue
		}
		v := phoneQuotaWindow{Label: strings.TrimSuffix(quotaLabel(w, i), tr("Quota")), UsedPercent: math.Max(0, math.Min(100, w.UsedPercent))}
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			v.ResetsAt = *w.ResetsAt
		}
		quota.Windows = append(quota.Windows, v)
	}
	if len(quota.Windows) == 0 {
		return nil
	}
	return []phoneAgentQuota{quota}
}
