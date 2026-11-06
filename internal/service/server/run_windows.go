//go:build windows

package server

import (
	"miya/internal/async"
	"miya/internal/service/process"
)

func Run(func(*async.Executor) *Service, func()) error { return process.ErrUnavailable }
