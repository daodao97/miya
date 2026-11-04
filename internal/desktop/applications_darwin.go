//go:build darwin && !ios && cgo

package desktop

/*
#include <stdlib.h>
int miya_desktop_application(const char *path, int icon, char **json);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"sort"
	"strings"
	"unsafe"
)

func Applications() ([]Application, error) {
	apps := applicationPaths(applicationDirs(), finderApplication)
	// Icons are small PNGs; keep the entire response comfortably below 2 MiB.
	budget := 600 << 10
	for i, app := range apps {
		path := C.CString(app.ID)
		var data *C.char
		withIcon := C.int(0)
		if budget > 0 {
			withIcon = 1
		}
		code := C.miya_desktop_application(path, withIcon, &data)
		C.free(unsafe.Pointer(path))
		if code != 0 {
			continue
		}
		var detail Application
		err := json.Unmarshal([]byte(C.GoString(data)), &detail)
		C.free(unsafe.Pointer(data))
		if err != nil {
			continue
		}
		if detail.Name != "" {
			apps[i].Name = detail.Name
		}
		apps[i].BundleID = detail.BundleID
		if size := (len(detail.Icon) + 2) / 3 * 4; size <= budget {
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
