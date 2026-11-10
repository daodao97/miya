package controller

import (
	"context"
	"slices"
	"time"

	"miya/internal/agent/catalog"
	"miya/internal/agent/cliupdate"
	"miya/internal/uiruntime"
)

func (c *Controller) agentUpdateEnv(id string) []string {
	switch id {
	case catalog.Claude:
		return append([]string(nil), c.ClaudeEnv...)
	case catalog.Pi:
		return append([]string(nil), c.Pi.Env...)
	}
	return nil
}

// EnsureAgentUpdates starts discovery when settings opens. All blocking work
// stays outside rendering and all state changes return to the controller loop.
func (c *Controller) EnsureAgentUpdates() {
	if c.Remote == nil {
		for id, info := range c.agentUpdates {
			if path, _ := c.AgentInstall(id); path != info.Path && !info.Updating {
				c.agentUpdatesAt = time.Time{}
				break
			}
		}
	}
	if time.Since(c.agentUpdatesAt) < 6*time.Hour {
		return
	}
	c.CheckAgentUpdates()
}

func (c *Controller) CheckAgentUpdates() {
	c.agentUpdatesAt = time.Now()
	if c.Remote != nil {
		c.forward("checkAgentUpdates", nil)
		return
	}
	if c.agentUpdates == nil {
		c.agentUpdates = map[string]cliupdate.Info{}
	}
	for _, d := range catalog.All() {
		id := d.ID
		old := c.agentUpdates[id]
		if old.Checking || old.Updating {
			continue
		}
		bin, _ := c.AgentInstall(id)
		env := c.agentUpdateEnv(id)
		parent := c.Ctx
		if parent == nil {
			parent = context.Background()
		}
		c.agentUpdates[id] = cliupdate.Info{Path: bin, Version: old.Version, Checking: true}
		go func() {
			ctx, cancel := context.WithTimeout(parent, 25*time.Second)
			defer cancel()
			info := cliupdate.Check(ctx, id, bin, env)
			if parent.Err() != nil {
				return
			}
			c.Dispatch(func() {
				// A changed custom launch command invalidates this discovery result.
				if path, _ := c.AgentInstall(id); path != bin || !slices.Equal(env, c.agentUpdateEnv(id)) {
					delete(c.agentUpdates, id)
					c.agentUpdatesAt = time.Time{}
				} else {
					c.agentUpdates[id] = info
				}
				uiruntime.Invalidate()
			})
		}()
	}
	uiruntime.Invalidate()
}

func (c *Controller) AgentUpdates() map[string]cliupdate.Info {
	out := make(map[string]cliupdate.Info, len(c.agentUpdates))
	for id, info := range c.agentUpdates {
		out[id] = info
	}
	return out
}

func (c *Controller) AgentUpdateBusy() bool {
	for _, info := range c.agentUpdates {
		if info.Updating {
			return true
		}
	}
	return false
}

func (c *Controller) UpgradeAgent(id string) {
	if _, ok := catalog.Lookup(id); !ok {
		return
	}
	if c.Remote != nil {
		c.forward("upgradeAgent", id)
		return
	}
	// Global installs can share folders; run only one updater at a time.
	if c.AgentUpdateBusy() {
		return
	}
	info := c.agentUpdates[id]
	if info.Checking || !info.Update {
		return
	}
	bin, _ := c.AgentInstall(id)
	if bin != info.Path {
		c.CheckAgentUpdates()
		return
	}
	env := c.agentUpdateEnv(id)
	parent := c.Ctx
	if parent == nil {
		parent = context.Background()
	}
	info.Updating, info.Error = true, ""
	c.agentUpdates[id] = info
	uiruntime.Invalidate()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
		defer cancel()
		result := cliupdate.Upgrade(ctx, id, bin, env)
		if parent.Err() != nil {
			return
		}
		c.Dispatch(func() {
			if path, _ := c.AgentInstall(id); path != bin || !slices.Equal(env, c.agentUpdateEnv(id)) {
				delete(c.agentUpdates, id)
				c.agentUpdatesAt = time.Time{}
			} else {
				c.agentUpdates[id] = result
			}
			c.forgetInstalls()
			// Running sessions keep their processes; subsequent launches use the
			// updated executable. Do not restart the shared Codex app-server here.
			uiruntime.Invalidate()
		})
	}()
}
