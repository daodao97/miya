package appmodel

import (
	"miya/internal/chat/model"
	"miya/internal/chat/presentation"
)

func ThreadRefTitle(t model.Thread) string { return presentation.ThreadRefTitle(t) }
func ThreadContextDisplay(s string) string { return presentation.ThreadContextDisplay(s) }
