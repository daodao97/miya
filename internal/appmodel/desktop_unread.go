package appmodel

import (
	model "miya/internal/model"
	notification "miya/internal/notification"
	strconv "strconv"
)

type DesktopRead struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
}

// Backend session IDs survive history reloads and reassigned sidebar IDs.
func DesktopUnreadKey(t Thread) string {
	return (notification.Notice{Thread: strconv.Itoa(t.ID), ServerID: t.ServerID, Agent: model.ThreadAgent(t)}).UnreadKey()
}
