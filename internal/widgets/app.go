package widgets

import (
	"strings"

	"miya/internal/msgfmt"
)

// imageSourcePath maps a Markdown image to something loadImage reads.
func ImageSourcePath(src string) string {
	if strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "file://") {
		return src
	}
	return msgfmt.NativeText(src)
}
