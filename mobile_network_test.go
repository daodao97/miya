package main

import (
	"context"
	"testing"
	"time"

	"miya/internal/remote"
)

// A send while the phone waits out a long backoff dials again at once.
func TestMobileSendSkipsReconnectBackoff(t *testing.T) {
	calls := make(chan func(), 32)
	old := runOnMain
	runOnMain = func(f func()) { calls <- f }
	t.Cleanup(func() { runOnMain = old })

	dials := make(chan struct{}, 4)
	m := testMobileApp()
	m.connected, m.conn = false, nil
	// errIncompatible waits 15 seconds before the next attempt.
	m.dialDesktop = func(context.Context, string) (*remote.Conn, error) {
		dials <- struct{}{}
		return nil, errIncompatible
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.cancel, m.wake = cancel, make(chan struct{}, 1)
	go m.run(ctx, m.generation, m.link, m.wake)

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
	m.draft = "hello"
	m.send()
	if m.sendErr != "未连接桌面端，正在重新连接…" {
		t.Fatalf("send error = %q", m.sendErr)
	}
	if !waitDial(2 * time.Second) {
		t.Fatal("send did not cut the reconnect wait short")
	}
}

func TestMobileNetworkPathChange(t *testing.T) {
	m := testMobileApp()
	m.connected, m.conn = false, nil
	m.cancel, m.wake = func() {}, make(chan struct{}, 1)
	woke := func() bool {
		select {
		case <-m.wake:
			return true
		default:
			return false
		}
	}

	m.networkPathChanged(true, "wifi")
	if woke() {
		t.Fatal("the first path report only records the path")
	}
	m.networkPathChanged(true, "wifi")
	if woke() {
		t.Fatal("an unchanged path retried")
	}
	m.networkPathChanged(false, "none")
	if woke() {
		t.Fatal("going offline retried")
	}
	m.networkPathChanged(true, "cellular")
	if !woke() {
		t.Fatal("coming back online did not retry")
	}
	m.networkPathChanged(true, "wifi")
	if !woke() {
		t.Fatal("moving to another interface did not retry")
	}

	// A live connection is probed, not woken or torn down.
	m.connected = true
	m.networkPathChanged(true, "cellular")
	if woke() || m.cancel == nil {
		t.Fatal("a live connection was retried")
	}

	// Nothing happens without a running connection.
	m.connected, m.cancel = false, nil
	m.networkPathChanged(true, "wifi")
	if woke() {
		t.Fatal("retried without a running connection")
	}
}
