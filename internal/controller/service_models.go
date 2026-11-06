package controller

import (
	codex "miya/internal/agent/codex/native"
	svcproto "miya/internal/service/protocol"
)

func snapshotModels(models []codex.Model) []svcproto.LegacyModel {
	if models == nil {
		return nil
	}
	out := make([]svcproto.LegacyModel, len(models))
	for i, model := range models {
		out[i] = svcproto.LegacyModel(model)
	}
	return out
}

func nativeModels(models []svcproto.LegacyModel) []codex.Model {
	if models == nil {
		return nil
	}
	out := make([]codex.Model, len(models))
	for i, model := range models {
		out[i] = codex.Model(model)
	}
	return out
}
