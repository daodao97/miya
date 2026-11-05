package main

import (
	"errors"
	"time"
)

// connectService connects to the service, starting it when none runs, and
// replaces one built from another executable once it is idle.
func connectService(workspace string) (*serviceClient, svcState, error) {
	sock := serviceSocket()
	cl, err := dialService(sock)
	if err != nil {
		if cl, err = startService(sock, workspace); err != nil {
			return nil, svcState{}, err
		}
	}
	exe, exeTime := executableInfo()
	var h svcHello
	if err := cl.Call("hello", nil, &h); err != nil {
		cl.Close()
		return nil, svcState{}, err
	}
	if h.Remote && h.Version != serviceVersion {
		cl.Close()
		return nil, svcState{}, errors.New("Incompatible server version. Update using the server installation process.")
	}
	if !h.Remote && (h.Version != serviceVersion || h.Exe != exe || !h.ExeTime.Equal(exeTime)) {
		switch {
		case !h.Busy:
			_ = cl.Call("quit", nil, nil)
			cl.Close()
			waitProcessGone(h.PID, 3*time.Second)
			if cl, err = startService(sock, workspace); err != nil {
				return nil, svcState{}, err
			}
		case h.Version == serviceVersion:
			// A turn runs in the older service: use it, and let it end once idle.
			cl.Notify("quitWhenIdle", nil)
		default:
			// Keep the old writer until its work ends, then let a waiting
			// window start the new service. Never create a second writer.
			cl.Notify("quitWhenIdle", nil)
			cl.Close()
			return nil, svcState{}, errors.New("Incompatible background service version; conversations are running")
		}
	}
	var st svcState
	if err := cl.Call("state", nil, &st); err != nil {
		cl.Close()
		return nil, svcState{}, err
	}
	return cl, st, nil
}

func startService(sock, workspace string) (*serviceClient, error) {
	if err := spawnService(workspace); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		time.Sleep(30 * time.Millisecond)
		cl, err := dialService(sock)
		if err == nil {
			return cl, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
	}
}
