package controller

import (
	errors "errors"
	appmodel "miya/internal/appmodel"
	time "time"
)

// connectService connects to the service, starting it when none runs, and
// replaces one built from another executable once it is idle.
func connectService(workspace string) (*appmodel.ServiceClient, appmodel.SvcState, error) {
	sock := appmodel.ServiceSocket()
	cl, err := appmodel.DialService(sock)
	if err != nil {
		if cl, err = startService(sock, workspace); err != nil {
			return nil, appmodel.SvcState{}, err
		}
	}
	exe, exeTime := executableInfo()
	var h appmodel.SvcHello
	if err := cl.Call("hello", nil, &h); err != nil {
		cl.Close()
		return nil, appmodel.SvcState{}, err
	}
	if h.Remote && h.Version != appmodel.ServiceVersion {
		cl.Close()
		return nil, appmodel.SvcState{}, errors.New("Incompatible server version. Update using the server installation process.")
	}
	if !h.Remote && (h.Version != appmodel.ServiceVersion || h.Exe != exe || !h.ExeTime.Equal(exeTime)) {
		switch {
		case !h.Busy:
			_ = cl.Call("quit", nil, nil)
			cl.Close()
			waitProcessGone(h.PID, 3*time.Second)
			if cl, err = startService(sock, workspace); err != nil {
				return nil, appmodel.SvcState{}, err
			}
		case h.Version == appmodel.ServiceVersion:
			// A turn runs in the older service: use it, and let it end once idle.
			cl.Notify("quitWhenIdle", nil)
		default:
			// Keep the old writer until its work ends, then let a waiting
			// window start the new service. Never create a second writer.
			cl.Notify("quitWhenIdle", nil)
			cl.Close()
			return nil, appmodel.SvcState{}, errors.New("Incompatible background service version; conversations are running")
		}
	}
	var st appmodel.SvcState
	if err := cl.Call("state", nil, &st); err != nil {
		cl.Close()
		return nil, appmodel.SvcState{}, err
	}
	return cl, st, nil
}

func startService(sock, workspace string) (*appmodel.ServiceClient, error) {
	if err := spawnService(workspace); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		time.Sleep(30 * time.Millisecond)
		cl, err := appmodel.DialService(sock)
		if err == nil {
			return cl, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
	}
}
