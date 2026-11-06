//go:build windows

package process

import (
	"errors"
	"time"
)

var ErrUnavailable = errors.New("miya: the background service is not available on Windows")

func Spawn(string) error                  { return ErrUnavailable }
func ExecutableInfo() (string, time.Time) { return "", time.Time{} }
func WaitGone(int, time.Duration)         {}
