package controller

import (
	"encoding/json"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/delegation"
	"miya/internal/delegation/coordinator"
	"miya/internal/interaction"
	mcpservice "miya/internal/mcp/service"
	"miya/internal/model"
)

func (c *Controller) MCP() *mcpservice.Service { return mcpservice.New(mcpHost{c}) }

type mcpHost struct{ *Controller }

func (h mcpHost) Cwd() string                                  { return h.Controller.Cwd }
func (h mcpHost) ReferenceSource() string                      { return h.Controller.ReferenceSource }
func (h mcpHost) ProjectThreads(project string) []model.Thread { return h.projectThreads(project) }
func (h mcpHost) ThreadTitle(t model.Thread) string            { return appmodel.ThreadRefTitle(t) }
func (h mcpHost) ResolveThread(id int) (model.Thread, []model.Message, bool) {
	return h.resolveThreadRef(id)
}
func (h mcpHost) ReferenceState(ref model.ThreadRef) (bool, bool) {
	_, known := h.ForkedReferences[ref]
	return known, h.referenceLoading[ref]
}
func (h mcpHost) PrefetchThread(id int) { h.prefetchThread(id) }
func (h mcpHost) ThreadPacket(t model.Thread, messages []model.Message) string {
	return threadPacket(t, messages)
}
func (h mcpHost) LastSubmitError() string { return h.Controller.LastSubmitError }
func (h mcpHost) ParentOf(id int) (int, bool) {
	parent, ok := h.DelegationParent[id]
	return parent, ok
}
func (h mcpHost) NoteDelegation(caller model.Thread, id int, task string) {
	h.noteHandoffs([]model.Thread{caller}, id, task)
	if h.DelegationParent == nil {
		h.DelegationParent = map[int]int{}
	}
	if _, ok := h.DelegationParent[id]; !ok && id != caller.ID {
		h.DelegationParent[id] = caller.ID
	}
}
func (h mcpHost) Collaboration() *coordinator.Coordinator { return h.Controller.Collaboration }
func (h mcpHost) StartCollaboration(t model.Thread, name string, a coordinator.Args) (any, error) {
	return h.startCollaboration(t, name, a)
}
func (h mcpHost) PrepareDelegation(t model.Thread, a coordinator.Args) (delegation.StartInput, error) {
	return h.prepareDelegation(t, a)
}
func (h mcpHost) AgentList() map[string]any                      { return h.agentList() }
func (h mcpHost) Automations() *automation.Runner                { return h.Auto }
func (h mcpHost) WaitUser(id json.RawMessage, ch chan bool) bool { return h.waitUser(id, ch) }
func (h mcpHost) OnMain(f func() (any, error)) (any, error)      { return h.onMain(f) }
func (h mcpHost) Approvals() []interaction.Request               { return h.Controller.Approvals() }
func (h mcpHost) SetApprovals(v []interaction.Request)           { h.Controller.SetApprovals(v) }

var _ mcpservice.Host = mcpHost{}
