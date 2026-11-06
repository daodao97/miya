package controller

import (
	"miya/internal/agent"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

func (c *Controller) CurrentAgentControls() agent.Controls { return c.CurrentBackend() }
func (c *Controller) AgentSettings() []agent.Setting {
	var out []agent.Setting
	for _, b := range c.Adapters() {
		out = append(out, b.Settings()...)
	}
	return out
}
func (c *Controller) SavedReferenceTitle(ref model.ThreadRef) (string, bool) {
	if ref.Source != c.ReferenceSource {
		return "", false
	}
	saved, ok := c.ForkedReferences[ref]
	return saved.Title, ok
}
func (c *Controller) ConfigureMirror(remote bool, reconnect func() (*svcclient.Client, svcproto.State, error)) {
	c.Mirror.RemoteHost = remote
	c.Mirror.Reconnect = reconnect
}
func (c *Controller) EnsurePhoneHost() {
	if c.Remote == nil && DesktopPhone == nil {
		DesktopPhone = NewPhoneHost(c)
	}
}

func (c *Controller) State() *appmodel.WindowState { return &c.WindowState }
