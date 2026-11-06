package controller

import (
	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/svcproto"
)

func (c *Controller) CurrentAgentControls() appmodel.AgentControls {
	b := c.CurrentBackend()
	return appmodel.AgentControls{Name: b.Name, Caps: b.Caps, Problem: func() (string, bool) { return b.Problem(c) }, Reconnect: func() { b.Reconnect(c) }, ModelChoices: func() []string { return b.ModelChoices(c) }, ModelLabel: func(v string) string { return b.ModelLabel(c, v) }, EffortChoices: func() []string { return b.EffortChoices(c) }, Model: func() string { return b.Model(c) }, Effort: func() string { return b.Effort(c) }, SetModel: func(v string) { b.SetModel(c, v) }, SetEffort: func(v string) { b.SetEffort(c, v) }}
}
func (c *Controller) AgentSettings() []appmodel.SettingControls {
	var out []appmodel.SettingControls
	for _, b := range AgentBackends {
		for _, setting := range b.Settings() {
			s := setting
			v := appmodel.SettingControls{ID: s.ID, Title: s.Title, Help: s.Help, Placeholder: s.Placeholder, Value: func() string { return s.Value(c) }, Save: func(value string) { s.Save(c, value) }}
			if s.Status != nil {
				v.Status = func() (string, bool) { return s.Status(c) }
			}
			out = append(out, v)
		}
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
