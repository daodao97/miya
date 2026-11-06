package controller

import (
	"errors"
	"time"

	"miya/internal/svcproto"
)

// connectService connects to the service, starting it when none runs, and
// replaces one built from another executable once it is idle.
func connectService(workspace string) (*svcproto.Client, svcproto.State, error) {
	sock := svcproto.Socket()
	cl, err := svcproto.Dial(sock)
	if err != nil {
		if cl, err = startService(sock, workspace); err != nil {
			return nil, svcproto.State{}, err
		}
	}
	exe, exeTime := executableInfo()
	var h svcproto.Hello
	if err := cl.Call("hello", nil, &h); err != nil {
		cl.Close()
		return nil, svcproto.State{}, err
	}
	if h.Remote && h.Version != svcproto.Version {
		cl.Close()
		return nil, svcproto.State{}, errors.New("Incompatible server version. Update using the server installation process.")
	}
	if !h.Remote && (h.Version != svcproto.Version || h.Exe != exe || !h.ExeTime.Equal(exeTime)) {
		switch {
		case !h.Busy:
			_ = cl.Call("quit", nil, nil)
			cl.Close()
			waitProcessGone(h.PID, 3*time.Second)
			if cl, err = startService(sock, workspace); err != nil {
				return nil, svcproto.State{}, err
			}
		case h.Version == svcproto.Version:
			// A turn runs in the older service: use it, and let it end once idle.
			cl.Notify("quitWhenIdle", nil)
		default:
			// Keep the old writer until its work ends, then let a waiting
			// window start the new service. Never create a second writer.
			cl.Notify("quitWhenIdle", nil)
			cl.Close()
			return nil, svcproto.State{}, errors.New("Incompatible background service version; conversations are running")
		}
	}
	var st svcproto.State
	if err := cl.Call("state", nil, &st); err != nil {
		cl.Close()
		return nil, svcproto.State{}, err
	}
	return cl, st, nil
}

func startService(sock, workspace string) (*svcproto.Client, error) {
	if err := spawnService(workspace); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		time.Sleep(30 * time.Millisecond)
		cl, err := svcproto.Dial(sock)
		if err == nil {
			return cl, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
	}
}
