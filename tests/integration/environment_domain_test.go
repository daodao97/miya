//go:build !mygo_ui

package integration

import (
	"errors"
	"os"
)

// isolated refuses the installed app's background service. Without a data
// directory of its own, a window factory would connect to it, and from a test
// binary it would replace that service or ask it to quit once idle.
func isolated() error {
	if os.Getenv("MIYA_DIR") != "" || os.Getenv("MIYA_ISOLATED_DIR") != "" {
		return nil
	}
	return errors.New("integration test reached the installed Miya background service; set MIYA_DIR")
}
