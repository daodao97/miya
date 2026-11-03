//go:build !darwin && !linux

package runtime

import "errors"

func SystemProcesses() ([]Process, error) {
	return nil, errors.New("当前系统尚不支持核验进程身份")
}
