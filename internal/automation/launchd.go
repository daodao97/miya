package automation

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"miya/internal/appdir"
)

const LaunchAgentLabel = "com.daodao.miya.service"

func LaunchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
}

func LaunchAgentPlist(exe string) []byte {
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
`, LaunchAgentLabel, esc(exe), esc(filepath.Join(appdir.Dir(), "service.log")), esc(filepath.Join(appdir.Dir(), "service.log")))
	return b.Bytes()
}

func LaunchdTarget() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func RemoveLaunchAgent() error {
	path := LaunchAgentPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	_ = exec.Command("/bin/launchctl", "bootout", LaunchdTarget()+"/"+LaunchAgentLabel).Run()
	return os.Remove(path)
}
