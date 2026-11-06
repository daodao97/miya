package controller

import (
	bytes "bytes"
	fmt "fmt"
	log "log"
	appdir "miya/internal/appdir"
	appmodel "miya/internal/appmodel"
	os "os"
	exec "os/exec"
	filepath "path/filepath"
	runtime "runtime"
	strings "strings"
	testing "testing"
)

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
		err = appmodel.RemoveAutomationAgent()
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
	path := appmodel.AutomationAgentPath()
	data := appmodel.AutomationAgentPlist(exe)
	service := appmodel.LaunchdTarget() + "/" + appmodel.AutomationAgentLabel
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
	if out, err := exec.Command("/bin/launchctl", "bootstrap", appmodel.LaunchdTarget(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
