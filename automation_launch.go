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

const automationAgentLabel = "com.daodao.miya.service"

func automationAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", automationAgentLabel+".plist")
}

// syncAutomationLaunchAgent installs the agent while an automation is on and
// removes it otherwise; an unchanged agent is left alone. Tests never touch
// it.
func syncAutomationLaunchAgent(enabled bool) {
	if testing.Testing() || runtime.GOOS != "darwin" {
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

func launchdTarget() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

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

func removeAutomationAgent() error {
	path := automationAgentPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	_ = exec.Command("/bin/launchctl", "bootout", launchdTarget()+"/"+automationAgentLabel).Run()
	return os.Remove(path)
}

func automationAgentPlist(exe string) []byte {
	var b bytes.Buffer
	esc := func(s string) string {
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
	}
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key><array><string>%s</string><string>-server</string></array>
	<key>RunAtLoad</key><true/>
	<key>ProcessType</key><string>Background</string>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, automationAgentLabel, esc(exe), esc(filepath.Join(appdir.Dir(), "service.log")), esc(filepath.Join(appdir.Dir(), "service.log")))
	return b.Bytes()
}
