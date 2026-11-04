//go:build !darwin && !linux

package runtime

import "errors"

func SystemProcesses() ([]Process, error) {
	return nil, errors.New("Process identity verification is not supported on this system")
}
