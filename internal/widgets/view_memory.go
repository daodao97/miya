package widgets

import (
	"miya/internal/memory/presentation"
	"time"
)

func MemoryKindLabel(s string) string  { return presentation.MemoryKindLabel(s) }
func MemoryStateLabel(s string) string { return presentation.MemoryStateLabel(s) }
func MemoryTime(t time.Time) string    { return presentation.MemoryTime(t) }
