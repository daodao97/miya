package codex

import (
	"strings"
)

func IsWriterConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already has an active writer") || strings.Contains(message, "already has a live local writer")
}
