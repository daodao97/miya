//go:build !darwin || ios

package nsui

import "time"

func systemIdleTime() (time.Duration, bool) { return 0, false }
