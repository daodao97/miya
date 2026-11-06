package controller

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"miya/internal/agentmeta"

	"miya/internal/account"
	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/phoneapi"
	"miya/internal/remote"
)

func (c *Controller) phoneAgentOptions() []phoneapi.AgentOptions {
	var out []phoneapi.AgentOptions
	for _, b := range c.Adapters() {
		a := phoneapi.AgentOptions{Agent: b.ID(), Current: remote.SendOptions{Model: b.Model(), Effort: b.Effort(), Policy: c.SelectedPermissionPolicy()}}
		a.Descriptor = agentmeta.For(b.ID())
		a.Commands = b.Commands(c.Cwd, false).PhoneChoices
		for _, option := range b.ModelOptions() {
			a.Models = append(a.Models, phoneapi.ModelOption{Value: option.Value, Label: option.Label, Efforts: option.Efforts, DefaultEffort: option.DefaultEffort})
		}
		out = append(out, a)
	}
	return out
}

func (c *Controller) PhoneSendRequest(r remote.Request, paths []string, agent string) (model.SendRequest, error) {
	if agent == "" {
		agent = appmodel.AgentCodex
	}
	if !model.RegisteredBackend(agent) {
		return model.SendRequest{}, errors.New("Unsupported agent")
	}
	req := model.SendRequest{Agent: agent, Text: r.Text, Attachments: paths, Steer: r.Steer}
	if r.Options == nil {
		return req, nil
	}
	for _, a := range c.phoneAgentOptions() {
		if a.Agent != agent {
			continue
		}
		var model *phoneapi.ModelOption
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

func (c *Controller) PhoneQuotas() []phoneapi.AgentQuota {
	q := c.QuotaLimits.RateLimits
	if v, ok := c.QuotaLimits.ByID[appmodel.CodexAccountLimitID]; ok {
		q = &v
	}
	if q == nil {
		return nil
	}
	windows := appmodel.ComposerQuotaWindows(c.QuotaLimits)
	// Preserve additional account windows without using model-specific buckets.
	for _, w := range []*account.QuotaWindow{q.Primary, q.Secondary} {
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
	quota := phoneapi.AgentQuota{Agent: appmodel.AgentCodex}
	for i, w := range windows {
		if math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) {
			continue
		}
		v := phoneapi.QuotaWindow{Label: strings.TrimSuffix(account.QuotaLabel(w, i), tr("Quota")), UsedPercent: math.Max(0, math.Min(100, w.UsedPercent))}
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			v.ResetsAt = *w.ResetsAt
		}
		quota.Windows = append(quota.Windows, v)
	}
	if len(quota.Windows) == 0 {
		return nil
	}
	return []phoneapi.AgentQuota{quota}
}
