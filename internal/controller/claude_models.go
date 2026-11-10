package controller

import (
	"encoding/json"
	"reflect"
	"time"

	claude "miya/internal/agent/claude/native"
	"miya/internal/prefs"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
)

// Claude Code reports its models as it initializes, with the slash commands.
// Miya keeps the last list, so the picker has it at once, and reads it again
// at most hourly, or each minute while it has none.
const (
	claudeModelsKept  = time.Hour
	claudeModelsRetry = time.Minute
)

// RefreshClaudeModels starts reading Claude Code's models when they are due.
func (c *Controller) RefreshClaudeModels() {
	wait := claudeModelsKept
	if len(c.claudeModels) == 0 {
		wait = claudeModelsRetry
	}
	if c.Cwd == "" || c.Ctx == nil || time.Since(c.claudeModelsTried) < wait {
		return
	}
	c.claudeModelsTried = time.Now()
	c.loadClaudeCommands(c.Cwd)
}

func (c *Controller) setClaudeModels(models []claude.Model) {
	if len(models) == 0 || reflect.DeepEqual(models, c.claudeModels) {
		return
	}
	c.claudeModels = models
	if c.Remote == nil {
		if data, err := json.Marshal(models); err == nil {
			prefs.Save("claudeModels", string(data))
		}
	}
	uiruntime.Invalidate()
}

func loadClaudeModels() []claude.Model {
	var models []claude.Model
	if s := prefs.Load("claudeModels"); s != "" {
		_ = json.Unmarshal([]byte(s), &models)
	}
	return models
}

func snapshotClaudeModels(models []claude.Model) []svcproto.ClaudeModel {
	if models == nil {
		return nil
	}
	out := make([]svcproto.ClaudeModel, len(models))
	for i, m := range models {
		out[i] = svcproto.ClaudeModel(m)
	}
	return out
}

func nativeClaudeModels(models []svcproto.ClaudeModel) []claude.Model {
	if models == nil {
		return nil
	}
	out := make([]claude.Model, len(models))
	for i, m := range models {
		out[i] = claude.Model(m)
	}
	return out
}
