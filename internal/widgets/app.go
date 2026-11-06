package widgets

import (
	"strings"

	msgfmt "miya/internal/chat/presentation"
)

// imageSourcePath maps a Markdown image to something loadImage reads.
func ImageSourcePath(src string) string {
	if strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "file://") {
		return src
	}
	return msgfmt.NativeText(src)
}
