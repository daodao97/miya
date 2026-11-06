package main

import (
	"bytes"
	"fmt"
	"log"
	"miya/internal/appdir"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// While an automation is on, a LaunchAgent starts the background service at
// login, so automations run after a restart with no window opened. It only
// starts the service: the service's lock keeps one running, and without
// KeepAlive launchd does not fight a window replacing the service with a
// newer one.

// syncAutomationLaunchAgent installs the agent while an automation is on and
// removes it otherwise; an unchanged agent is left alone. Tests never touch
// it, nor does an isolated instance: the login item belongs to the installed
// app, and an isolated one would replace or remove it.
func syncAutomationLaunchAgent(enabled bool) {
	if testing.Testing() || runtime.GOOS != "darwin" || appdir.Isolated() {
		return
	}
	var err error
	if enabled {
		err = installAutomationAgent()
	} else {
		err = removeAutomationAgent()
	}
	if err != nil {
		log.Printf("miya: automation login item: %v", err)
	}
}

func installAutomationAgent() error {
	exe, _ := executableInfo()
	if exe == "" {
		return fmt.Errorf("no executable")
	}
	path := automationAgentPath()
	data := automationAgentPlist(exe)
	service := launchdTarget() + "/" + automationAgentLabel
	loaded := exec.Command("/bin/launchctl", "print", service).Run() == nil
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) && loaded {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	if loaded {
		_ = exec.Command("/bin/launchctl", "bootout", service).Run()
	}
	// Bootstrapping runs the agent now; the running service holds the lock,
	// so the new one exits at once.
	if out, err := exec.Command("/bin/launchctl", "bootstrap", launchdTarget(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
