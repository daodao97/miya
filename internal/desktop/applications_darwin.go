//go:build darwin && !ios

package desktop

import (
	"context"
	"errors"
	"os/exec"
	"sort"
	"strings"

	dc "github.com/egoist/mygo/plugins/desktopcontrol"
)

func Applications() ([]Application, error) {
	apps := applicationPaths(applicationDirs(), finderApplication)
	// Icons are small PNGs; keep the entire response comfortably below 2 MiB.
	budget := 600 << 10
	for i, app := range apps {
		size := 0
		if budget > 0 {
			size = 48
		}
		detail, err := dc.ApplicationInfo(app.ID, size)
		if err != nil {
			continue
		}
		if detail.Name != "" {
			apps[i].Name = detail.Name
		}
		apps[i].BundleID = detail.BundleID
		if size := (len(detail.Icon) + 2) / 3 * 4; size <= budget && len(detail.Icon) <= 16384 {
			apps[i].Icon = detail.Icon
			budget -= size
		}
	}
	sort.SliceStable(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	return apps, nil
}

func OpenApplication(ctx context.Context, id string) error {
	if !listedApplication(id, applicationPaths(applicationDirs(), finderApplication)) {
		return errors.New("This app is no longer available. Refresh the app list.")
	}
	// Argument passing preserves spaces and punctuation without shell evaluation.
	if err := exec.CommandContext(ctx, "/usr/bin/open", "-a", id).Run(); err != nil {
		return errors.New("Unable to open the app on your Mac. Refresh the list and retry.")
	}
	return nil
}
