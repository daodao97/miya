package main

func (c *chatController) runtimes() *runtimeManager {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if c.runtime == nil {
		c.runtime = newRuntimeManager(c.ctx, c.runtimePath)
		if c.cancel != nil {
			cancel := c.cancel
			manager := c.runtime
			c.cancel = func() { cancel(); manager.Close() }
		}
	}
	return c.runtime
}

func (c *chatController) runtimeBusy() bool {
	c.runtimeMu.Lock()
	manager := c.runtime
	c.runtimeMu.Unlock()
	return manager != nil && manager.Busy()
}
