package controller

import (
	errors "errors"
	fmt "fmt"
	math "math"
	appmodel "miya/internal/appmodel"
	remote "miya/internal/remote"
	slices "slices"
	strings "strings"
)

func (c *Controller) phoneAgentOptions() []appmodel.PhoneAgentOptions {
	var out []appmodel.PhoneAgentOptions
	for _, b := range AgentBackends {
		a := appmodel.PhoneAgentOptions{Agent: b.ID(), Current: remote.SendOptions{Model: b.Model(c), Effort: b.Effort(c), Policy: c.SelectedPermissionPolicy()}}
		var choices []string
		if b.ID() == appmodel.AgentCodex && c.Models == nil {
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
			m := appmodel.PhoneModelOption{Value: value, Label: b.ModelLabel(c, value)}
			if b.ID() == appmodel.AgentClaude {
				m.Efforts = b.EffortChoices(c)
			} else if c.Models != nil {
				for _, model := range c.Models() {
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

func (c *Controller) PhoneSendRequest(r remote.Request, paths []string, agent string) (appmodel.SendRequest, error) {
	if agent == "" {
		agent = appmodel.AgentCodex
	}
	if agent != appmodel.AgentCodex && agent != appmodel.AgentClaude {
		return appmodel.SendRequest{}, errors.New("Unsupported agent")
	}
	req := appmodel.SendRequest{Agent: agent, Text: r.Text, Attachments: paths, Steer: r.Steer}
	if r.Options == nil {
		return req, nil
	}
	for _, a := range c.phoneAgentOptions() {
		if a.Agent != agent {
			continue
		}
		var model *appmodel.PhoneModelOption
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

func (c *Controller) PhoneQueueAction(r remote.Request) error {
	target := c.PhoneThreadFor(r)
	if !target.Found {
		return errors.New("Conversation is no longer on the desktop")
	}
	if target.cwd != c.Cwd {
		return errors.New("Send a message in this project before managing the queue")
	}
	switch r.Op {
	case "dequeue":
		c.RemoveQueued(target.Thread.ID, r.QueuedID)
	case "steer-queued":
		c.SteerQueued(target.Thread.ID, r.QueuedID)
	case "drain-queue":
		c.ResumeQueue(target.Thread.ID)
	default:
		return errors.New("Unknown queue action")
	}
	return nil
}

func (c *Controller) PhoneQuotas() []appmodel.PhoneAgentQuota {
	q := c.QuotaLimits.RateLimits
	if v, ok := c.QuotaLimits.ByID[appmodel.CodexAccountLimitID]; ok {
		q = &v
	}
	if q == nil {
		return nil
	}
	windows := appmodel.ComposerQuotaWindows(c.QuotaLimits)
	// Preserve additional account windows without using model-specific buckets.
	for _, w := range []*appmodel.QuotaWindow{q.Primary, q.Secondary} {
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
	quota := appmodel.PhoneAgentQuota{Agent: appmodel.AgentCodex}
	for i, w := range windows {
		if math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) {
			continue
		}
		v := appmodel.PhoneQuotaWindow{Label: strings.TrimSuffix(appmodel.QuotaLabel(w, i), tr("Quota")), UsedPercent: math.Max(0, math.Min(100, w.UsedPercent))}
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			v.ResetsAt = *w.ResetsAt
		}
		quota.Windows = append(quota.Windows, v)
	}
	if len(quota.Windows) == 0 {
		return nil
	}
	return []appmodel.PhoneAgentQuota{quota}
}
