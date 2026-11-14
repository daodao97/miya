package integration

import (
	"context"
	mobile "miya/internal/phone/client"
	"miya/internal/remote"
	"testing"
	"time"
)

// A send while the phone waits out a long backoff dials again at once.
func TestMobileSendSkipsReconnectBackoff(t *testing.T) {
	calls := make(chan func(), 32)

	dials := make(chan struct{}, 4)
	m := testPhoneClient()
	m.Dispatch = func(f func()) { calls <- f }
	m.Connected, m.Conn = false, nil
	// errIncompatible waits 15 seconds before the next attempt.
	m.DialDesktop = func(context.Context, string) (*remote.Conn, error) {
		dials <- struct{}{}
		return nil, mobile.ErrIncompatible
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.Cancel, m.Wake = cancel, make(chan struct{}, 1)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx, m.Generation, m.Link, m.Wake) }()
	// The global dispatch hook belongs to this test. Let the dial loop exit
	// before Cleanup restores it, otherwise the next UI test can inherit a race.
	defer func() { cancel(); <-done }()

	waitDial := func(d time.Duration) bool {
		timeout := time.After(d)
		for {
			select {
			case f := <-calls:
				f()
			case <-dials:
				return true
			case <-timeout:
				return false
			}
		}
	}
	if !waitDial(2 * time.Second) {
		t.Fatal("first dial did not happen")
	}
	m.Draft = "hello"
	m.Send()
	if m.SendErr != "Not connected to desktop. Reconnecting…" {
		t.Fatalf("send error = %q", m.SendErr)
	}
	if !waitDial(2 * time.Second) {
		t.Fatal("send did not cut the reconnect wait short")
	}
}

func TestMobileNetworkPathChange(t *testing.T) {
	m := testPhoneClient()
	m.Connected, m.Conn = false, nil
	m.Cancel, m.Wake = func() {}, make(chan struct{}, 1)
	woke := func() bool {
		select {
		case <-m.Wake:
			return true
		default:
			return false
		}
	}

	m.NetworkPathChanged(true, "wifi")
	if woke() {
		t.Fatal("the first path report only records the path")
	}
	m.NetworkPathChanged(true, "wifi")
	if woke() {
		t.Fatal("an unchanged path retried")
	}
	m.NetworkPathChanged(false, "none")
	if woke() {
		t.Fatal("going offline retried")
	}
	m.NetworkPathChanged(true, "cellular")
	if !woke() {
		t.Fatal("coming back online did not retry")
	}
	m.NetworkPathChanged(true, "wifi")
	if !woke() {
		t.Fatal("moving to another interface did not retry")
	}

	// A live connection is probed, not woken or torn down.
	m.Connected = true
	m.NetworkPathChanged(true, "cellular")
	if woke() || m.Cancel == nil {
		t.Fatal("a live connection was retried")
	}

	// Nothing happens without a running connection.
	m.Connected, m.Cancel = false, nil
	m.NetworkPathChanged(true, "wifi")
	if woke() {
		t.Fatal("retried without a running connection")
	}
}
