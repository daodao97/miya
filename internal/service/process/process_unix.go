//go:build !windows

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"miya/internal/appdir"
)

// spawnService starts the service as a process of its own, in a session of
// its own, so that it outlives the window that started it.
func Spawn(workspace string) error {
	exe, _ := ExecutableInfo()
	if exe == "" {
		return errors.New("miya: no executable to start the service")
	}
	dir := appdir.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "-server")
	cmd.Env = append(os.Environ(), "QUICKGUI_WORKSPACE="+workspace)
	cmd.Dir = workspace
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// SpawnReplacement preserves this service's mode, arguments and environment,
// using its original installation path after an atomic update.
func SpawnReplacement(exe string) error {
	f, err := os.OpenFile(filepath.Join(appdir.Dir(), "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Supervised checks the unit's actual main PID. INVOCATION_ID alone is not
// sufficient: a server launched from a systemd-managed shell inherits it too.
func Supervised() bool {
	if runtime.GOOS != "linux" || os.Getenv("INVOCATION_ID") == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "systemctl", "--user", "show", "miya.service", "--property=MainPID", "--value").Output()
	return err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(os.Getpid())
}

func ExecutableInfo() (string, time.Time) {
	exe, err := os.Executable()
	if err != nil {
		return "", time.Time{}
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	st, err := os.Stat(exe)
	if err != nil {
		return exe, time.Time{}
	}
	return exe, st.ModTime()
}

func WaitGone(pid int, timeout time.Duration) {
	for deadline := time.Now().Add(timeout); pid > 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
}
