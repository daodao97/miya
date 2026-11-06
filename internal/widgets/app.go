package widgets

import (
	appmodel "miya/internal/appmodel"
	strings "strings"
)

// imageSourcePath maps a Markdown image to something loadImage reads.
func ImageSourcePath(src string) string {
	if strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "file://") {
		return src
	}
	return appmodel.NativeText(src)
}
