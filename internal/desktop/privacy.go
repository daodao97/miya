package desktop

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Privacy belongs to a single desktop lease. Done also closes when the person
// at the Mac presses the recovery shortcut or the helper loses its heartbeat.
type Privacy interface {
	Close() error
	Done() <-chan struct{}
}

type privacyProcess struct {
	done     chan struct{}
	stopping chan struct{}
	stop     sync.Once
}

func (p *privacyProcess) Done() <-chan struct{} { return p.done }
func (p *privacyProcess) Close() error {
	p.stop.Do(func() { close(p.stopping) })
	<-p.done
	return nil
}

// A previous helper must restore its tables before a new helper saves them.
var privacyGate = make(chan struct{}, 1)

func StartPrivacy(ctx context.Context) (Privacy, error) {
	if !Available() {
		return nil, Authorize()
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return startPrivacyProcess(ctx, exec.Command(exe, "-desktop-privacy"))
}

func startPrivacyProcess(ctx context.Context, cmd *exec.Cmd) (Privacy, error) {
	select {
	case privacyGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, errors.New("Another remote desktop session is using privacy mode. Close it first.")
	}
	release := func() { <-privacyGate }
	stdin, err := cmd.StdinPipe()
	if err != nil {
		release()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		release()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		release()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		release()
		return nil, err
	}
	p := &privacyProcess{done: make(chan struct{}), stopping: make(chan struct{})}
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(io.LimitReader(stdout, 512)).ReadString('\n')
		ready <- line
	}()
	go func() {
		_ = cmd.Wait()
		_ = stdin.Close()
		release()
		close(p.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
		case <-p.stopping:
		case <-p.done:
			return
		}
		_ = stdin.Close()
		select {
		case <-p.done:
		case <-time.After(4 * time.Second):
			_ = cmd.Process.Kill()
			<-p.done
		}
	}()
	// EOF and loss of this heartbeat both make the helper restore and exit.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.stopping:
				return
			case <-p.done:
				return
			case <-ticker.C:
				if _, err := stdin.Write([]byte{1}); err != nil {
					return
				}
			}
		}
	}()
	// Only the dedicated helper may be killed; never the Miya service. Its
	// normal EOF path restores first. Bound cleanup if native APIs get stuck.
	stop := func() {
		_ = stdin.Close()
		select {
		case <-p.done:
		case <-time.After(4 * time.Second):
			_ = cmd.Process.Kill()
			<-p.done
		}
	}
	select {
	case line := <-ready:
		if line == "ready\n" && ctx.Err() == nil {
			select {
			case <-p.done:
			default:
				return p, nil
			}
		}
		stop()
		return nil, errors.New("Privacy mode is unavailable. Check your displays and the Mac recovery shortcut.")
	case <-ctx.Done():
		stop()
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		stop()
		return nil, errors.New("Privacy mode did not respond. Your Mac screen has been restored.")
	}
}
