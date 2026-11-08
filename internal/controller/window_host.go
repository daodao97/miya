package controller

import (
	"miya/internal/agent"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

func (c *Controller) CurrentAgentControls() agent.Controls {
	if t := c.Current(); t.Contact != nil {
		return contactControls{Controls: c.CurrentBackend(), c: c, id: t.ID}
	}
	return c.CurrentBackend()
}

type contactControls struct {
	agent.Controls
	c  *Controller
	id int
}

func (b contactControls) Name() string {
	t := b.c.ThreadWithID(b.id)
	if p, ok := b.c.ContactSnapshot().Find(t.Contact.BotID); ok {
		return p.Name
	}
	return t.Contact.Name
}
func (b contactControls) Model() string      { return b.c.ThreadWithID(b.id).LaunchModel }
func (b contactControls) Effort() string     { return b.c.ThreadWithID(b.id).LaunchEffort }
func (b contactControls) SetModel(v string)  { b.c.setContactChoice(b.id, v, "") }
func (b contactControls) SetEffort(v string) { b.c.setContactChoice(b.id, b.Model(), v) }

func (b contactControls) ModelChoices() []string {
	t := b.c.ThreadWithID(b.id)
	var out []string
	found := false
	for _, option := range b.c.AgentModels(model.ThreadAgent(t)) {
		out = append(out, option.Value)
		found = found || option.Value == b.Model()
	}
	if !found {
		out = append(out, b.Model())
	}
	return out
}

func (b contactControls) EffortChoices() []string {
	t := b.c.ThreadWithID(b.id)
	for _, option := range b.c.AgentModels(model.ThreadAgent(t)) {
		if option.Value == b.Model() {
			return append([]string(nil), option.Efforts...)
		}
	}
	return nil
}

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
