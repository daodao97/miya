package appmodel

import (
	"miya/internal/chat/model"
	"miya/internal/notification"
)

type DesktopRead = notification.DesktopRead

func DesktopUnreadKey(t model.Thread) string { return notification.DesktopUnreadKey(t) }
