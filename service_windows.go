//go:build windows

package main

import (
	"errors"
	"time"
)

// The background service needs Unix sockets and sessions; on Windows the
// window runs everything itself.
var errNoService = errors.New("miya: the background service is not available on Windows")

func runService() error                   { return errNoService }
func spawnService(string) error           { return errNoService }
func executableInfo() (string, time.Time) { return "", time.Time{} }
func waitProcessGone(int, time.Duration)  {}
