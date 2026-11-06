package controller

import (
	"miya/internal/agent"
	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/svcproto"
)

func (c *Controller) CurrentAgentControls() agent.Controls { return c.CurrentBackend() }
func (c *Controller) AgentSettings() []agent.Setting {
	var out []agent.Setting
	for _, b := range c.Adapters() {
		out = append(out, b.Settings()...)
	}
	return out
}
func (c *Controller) KnownClaudeCommands(cwd string) []model.ComposerChoice {
	return c.ClaudeCmdCache[cwd].Choice
}
func (c *Controller) SavedReferenceTitle(ref model.ThreadRef) (string, bool) {
	if ref.Source != c.ReferenceSource {
		return "", false
	}
	saved, ok := c.ForkedReferences[ref]
	return saved.Title, ok
}
func (c *Controller) ConfigureMirror(remote bool, reconnect func() (*svcproto.Client, svcproto.State, error)) {
	c.Mirror.RemoteHost = remote
	c.Mirror.Reconnect = reconnect
}
func (c *Controller) EnsurePhoneHost() {
	if c.Remote == nil && DesktopPhone == nil {
		DesktopPhone = NewPhoneHost(c)
	}
}

func (c *Controller) State() *appmodel.WindowState { return &c.WindowState }
