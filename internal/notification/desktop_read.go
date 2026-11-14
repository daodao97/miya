package notification

import (
	"strconv"

	"miya/internal/chat/model"
)

type DesktopRead struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
}

// Backend session IDs survive history reloads and reassigned sidebar IDs.
func DesktopUnreadKey(t model.Thread) string {
	return (Notice{Thread: strconv.Itoa(t.ID), ServerID: t.ServerID, Agent: model.ThreadAgent(t)}).UnreadKey()
}
