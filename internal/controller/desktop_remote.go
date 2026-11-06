package controller

func (c *Controller) RemoteHost() bool {
	return c != nil && c.Remote != nil && (c.Remote.Wire() != nil || c.Mirror != nil && c.Mirror.RemoteHost)
}
