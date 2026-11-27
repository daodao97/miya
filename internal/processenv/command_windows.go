package processenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

func prepareNative(cmd *exec.Cmd) {
	if cmd.Err != nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if cmd.Cancel != nil {
		original := cmd.Cancel
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return original()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			kill := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
			kill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
			if err := kill.Run(); err == nil {
				return nil
			}
			return original()
		}
	}
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
	switch strings.ToLower(filepath.Ext(cmd.Path)) {
	case ".cmd", ".bat":
	default:
		return
	}
	// npm and agent shims can run Node directly. This avoids cmd.exe parsing
	// prompts, schema JSON and paths as shell syntax.
	if target := NodeShim(cmd.Path); target != "" {
		node := filepath.Join(filepath.Dir(cmd.Path), "node.exe")
		if st, err := os.Stat(node); err != nil || st.IsDir() {
			node, _ = LookPath("node.exe")
		}
		if node != "" {
			cmd.Path = node
			cmd.Args = append([]string{node, target}, cmd.Args[1:]...)
			return
		}
	}
	// Other batch launchers require cmd.exe. Reject expansion and line breaks
	// rather than silently changing their arguments or executing another command.
	var line strings.Builder
	line.WriteByte('"')
	for i, a := range cmd.Args {
		if strings.ContainsAny(a, "%\r\n\x00\"") {
			cmd.Err = errors.New("batch arguments contain unsupported expansion, quotes or line breaks; use the executable or Node entry point")
			return
		}
		if i > 0 {
			line.WriteByte(' ')
		}
		line.WriteByte('"')
		line.WriteString(a)
		line.WriteByte('"')
	}
	line.WriteByte('"')
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmd.Path = shell
	cmd.Args = []string{shell, "/d", "/v:off", "/s", "/c", line.String()}
	cmd.SysProcAttr.CmdLine = `"` + shell + `" /d /v:off /s /c ` + line.String()
}

func platformDirs(home string) []string {
	dirs := []string{filepath.Join(os.Getenv("APPDATA"), "npm"), filepath.Join(home, ".local", "bin"), filepath.Join(home, ".bun", "bin"), filepath.Join(os.Getenv("LOCALAPPDATA"), "pnpm"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Volta", "bin"), filepath.Join(os.Getenv("ProgramFiles"), "nodejs")}
	for _, item := range []struct {
		root registry.Key
		path string
	}{{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`}, {registry.CURRENT_USER, `Environment`}} {
		key, err := registry.OpenKey(item.root, item.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		value, _, err := key.GetStringValue("Path")
		key.Close()
		if err != nil {
			continue
		}
		if expanded, err := registry.ExpandString(value); err == nil {
			value = expanded
		}
		dirs = append(dirs, filepath.SplitList(value)...)
	}
	return dirs
}
