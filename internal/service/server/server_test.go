package server

import (
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"miya/internal/async"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

type draftTestHost struct {
	Host
	started chan struct{}
	release chan struct{}
	err     error
	cwd     string
}

func (h *draftTestHost) ServiceRequest(req svcproto.Request) (any, error) {
	switch req.Op {
	case "automationDraft":
		return async.Reply(func() (any, error) {
			close(h.started)
			<-h.release
			return "generated settings", h.err
		}), nil
	case "workspace":
		return nil, json.Unmarshal(req.Args, &h.cwd)
	}
	return nil, errors.New("unexpected operation: " + req.Op)
}

func (h *draftTestHost) SnapshotState() svcproto.State { return svcproto.State{Cwd: h.cwd} }
func (h *draftTestHost) ServiceStop()                  {}

func TestLocalRequestsContinueWhileDrafting(t *testing.T) {
	for _, draftErr := range []error{nil, errors.New("generation failed")} {
		name := "success"
		if draftErr != nil {
			name = "error"
		}
		t.Run(name, func(t *testing.T) {
			h := &draftTestHost{started: make(chan struct{}), release: make(chan struct{}), err: draftErr, cwd: "/original"}
			ex := async.NewExecutor()
			s := New(h, ex)
			loopDone := make(chan struct{})
			go func() { ex.Loop(s.Quit); close(loopDone) }()
			t.Cleanup(func() { s.Stop(); <-loopDone })
			conn, peer := net.Pipe()
			cl := svcclient.NewClient(conn)
			peerDone := make(chan struct{})
			go func() { s.HandleConn(&Peer{conn: peer}); close(peerDone) }()
			t.Cleanup(func() { cl.Close(); <-peerDone })
			release := sync.OnceFunc(func() { close(h.release) })
			t.Cleanup(release)

			draftDone := make(chan error, 1)
			go func() {
				var result string
				err := cl.Call("automationDraft", nil, &result)
				if err == nil && result != "generated settings" {
					err = errors.New("draft reply reached the wrong request")
				}
				draftDone <- err
			}()
			select {
			case <-h.started:
			case <-time.After(2 * time.Second):
				t.Fatal("draft did not start")
			}

			pageDone := make(chan error, 1)
			go func() {
				// A page change is a notification followed by a request on the
				// same connection. Both must run before generation completes,
				// and the state must reflect the notification sent first.
				cl.Notify("workspace", "/other")
				var state svcproto.State
				err := cl.Call("state", nil, &state)
				if err == nil && state.Cwd != "/other" {
					err = errors.New("page state did not reflect the preceding notification")
				}
				pageDone <- err
			}()
			select {
			case err := <-pageDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("page change blocked behind automation generation")
			}
			select {
			case <-draftDone:
				t.Fatal("draft completed before it was released")
			default:
			}
			release()
			select {
			case err := <-draftDone:
				if draftErr == nil && err != nil || draftErr != nil && (err == nil || err.Error() != draftErr.Error()) {
					t.Fatalf("draft reply error = %v, want %v", err, draftErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("draft reply was lost after switching pages")
			}
		})
	}
}
