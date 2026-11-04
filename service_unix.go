//go:build !windows

package main

import (
	"errors"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// runService is the executable run with -server: it owns the agents until
// nothing needs it.
func runService() error {
	return runServiceWithOptions(serverOptions{})
}

func runServiceWithOptions(options serverOptions) error {
	servingRemote = options.Persistent
	dir := serviceDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "service.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("miya: a service already runs")
	}
	sock := serviceSocket()
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	os.Chmod(sock, 0o600)
	ex := newExecutor()
	runOnMain = ex.push
	miyaMCPEnabled = true
	var s *service
	ready := make(chan struct{})
	ex.push(func() {
		c := newChatController()
		s = newService(c, ex)
		s.persistent = options.Persistent
		c.startAutomations(automationsPath(), true)
		s.exe, s.exeTime = executableInfo()
		setInvalidateHook(s.markDirty)
		// Phones read the service's controller, which holds every project.
		desktopPhone = newPhoneHost(c)
		if options.Persistent {
			desktopPhone.service = s
			desktopPhone.start()
		}
		close(ready)
	})
	go func() {
		<-ready
		go s.serve(ln)
		go s.publishLoop()
		go s.watchIdle(15 * time.Second)
		log.Printf("miya: service on %s (pid %d)", sock, os.Getpid())
	}()
	quit := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-ready
		select {
		case <-s.quit:
		case <-signals:
			ex.push(s.stop)
			<-s.quit
		}
		close(quit)
	}()
	ex.loop(quit)
	ln.Close()
	os.Remove(sock)
	runCleanups()
	log.Printf("miya: service stopped")
	return nil
}

// spawnService starts the service as a process of its own, in a session of
// its own, so that it outlives the window that started it.
func spawnService(workspace string) error {
	exe, _ := executableInfo()
	if exe == "" {
		return errors.New("miya: no executable to start the service")
	}
	dir := serviceDir()
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

func executableInfo() (string, time.Time) {
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

func waitProcessGone(pid int, timeout time.Duration) {
	for deadline := time.Now().Add(timeout); pid > 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
}
