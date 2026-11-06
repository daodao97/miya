package controller

import (
	appmodel "miya/internal/appmodel"
)

func (c *Controller) Runtimes() *appmodel.RuntimeManager {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if c.runtime == nil {
		c.runtime = appmodel.NewRuntimeManager(c.Ctx, c.runtimePath)
		if c.Cancel != nil {
			cancel := c.Cancel
			manager := c.runtime
			c.Cancel = func() { cancel(); manager.Close() }
		}
	}
	return c.runtime
}

func (c *Controller) runtimeBusy() bool {
	c.runtimeMu.Lock()
	manager := c.runtime
	c.runtimeMu.Unlock()
	return manager != nil && manager.Busy()
}
