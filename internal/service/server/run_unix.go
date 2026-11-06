//go:build !windows

package server

import (
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"miya/internal/appdir"
	"miya/internal/async"
	svcproto "miya/internal/service/protocol"
)

func Run(factory func(*async.Executor) *Service, cleanup func()) error {
	dir := appdir.Dir()
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
	sock := svcproto.Socket()
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	os.Chmod(sock, 0o600)
	ex := async.NewExecutor()
	var s *Service
	ready := make(chan struct{})
	ex.Push(func() { s = factory(ex); close(ready) })
	go func() {
		<-ready
		go s.Serve(ln)
		go s.PublishLoop()
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
		case <-s.Quit:
		case <-signals:
			ex.Push(s.Stop)
			<-s.Quit
		}
		close(quit)
	}()
	ex.Loop(quit)
	ln.Close()
	os.Remove(sock)
	if cleanup != nil {
		cleanup()
	}
	log.Printf("miya: service stopped")
	return nil
}
