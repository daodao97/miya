package remote

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/tailscale/tailcat"
)

// Options configures the desktop side.
type Options struct {
	// Service and Terminal are optional desktop-client streams. They are
	// private Tailcat ports, protected by the same pairing capability.
	Service  func(context.Context, *Conn)
	Terminal func(context.Context, *Conn)
	// Files serves ordinary HTTP over the same private overlay on FilesPort.
	Files http.Handler
	// StateDir persists the identity and relay, keeping the QR code stable
	// across restarts. Empty uses an ephemeral identity.
	StateDir string
	// Serve runs on its own goroutine for each phone and owns the connection
	// until it returns. ctx ends when the bridge closes.
	Serve func(ctx context.Context, conn *Conn)
	// OnPeers reports the number of connected phones. It must not block.
	OnPeers func(int)
}

// Bridge serves the desktop to phones over its Tailcat overlay.
type Bridge struct {
	attachments attachmentHub
	server      *tailcat.Server
	listener    net.Listener
	listeners   []net.Listener
	files       *http.Server
	lock        *os.File
	ctx         context.Context
	cancel      context.CancelFunc
	options     Options

	mu     sync.Mutex
	conns  map[*Conn]bool
	closed bool
}

func Start(ctx context.Context, options Options) (*Bridge, error) {
	b := &Bridge{server: &tailcat.Server{Logf: quiet}, conns: map[*Conn]bool{}, options: options}
	if options.StateDir != "" {
		identity, lock, err := loadIdentity(ctx, options.StateDir)
		if err != nil {
			return nil, err
		}
		b.lock = lock
		b.server.Key, b.server.PresharedKey, b.server.Region = identity.Key, identity.PresharedKey, identity.Region
	}
	ln, err := b.server.Listen(ctx, "tcp", fmt.Sprintf(":%d", Port))
	if err != nil {
		b.server.Close()
		releaseIdentity(b.lock)
		return nil, err
	}
	b.listener = ln
	b.ctx, b.cancel = context.WithCancel(context.Background())
	for _, stream := range []struct {
		port  uint16
		serve func(context.Context, *Conn)
	}{{ServicePort, options.Service}, {TerminalPort, options.Terminal}} {
		if stream.serve == nil {
			continue
		}
		listener, err := b.server.Listen(ctx, "tcp", fmt.Sprintf(":%d", stream.port))
		if err != nil {
			b.Close()
			return nil, err
		}
		b.listeners = append(b.listeners, listener)
		go b.acceptStream(listener, stream.serve, false)
	}
	if options.StateDir != "" {
		if err := saveIdentity(options.StateDir, b.server); err != nil {
			b.Close()
			return nil, err
		}
	}
	{
		files, err := b.server.Listen(ctx, "tcp", fmt.Sprintf(":%d", FilesPort))
		if err != nil {
			b.Close()
			return nil, err
		}
		b.files = &http.Server{Handler: b.attachments.handler(options.Files), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
		go b.files.Serve(files)
	}
	go b.attachments.reap(b.ctx)
	go b.acceptStream(b.listener, b.options.Serve, true)
	return b, nil
}

// Link is the miya:// link the QR code shows.
func (b *Bridge) Link() string { return Link(b.server.TailcatAddr()) }

func (b *Bridge) acceptStream(listener net.Listener, serve func(context.Context, *Conn), attachments bool) {
	for {
		nc, err := listener.Accept()
		if err != nil {
			return
		}
		conn := newConn(nc, nil)
		if attachments {
			session, err := b.attachments.newSession()
			if err != nil {
				conn.Close()
				continue
			}
			conn.localAttachments = session
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			conn.Close()
			return
		}
		b.conns[conn] = true
		n := len(b.conns)
		b.mu.Unlock()
		b.peers(n)
		go func() {
			defer func() {
				conn.Close()
				b.mu.Lock()
				delete(b.conns, conn)
				n := len(b.conns)
				closed := b.closed
				b.mu.Unlock()
				if !closed {
					b.peers(n)
				}
			}()
			if serve != nil {
				serve(b.ctx, conn)
			}
		}()
	}
}

func (b *Bridge) peers(n int) {
	if b.options.OnPeers != nil {
		b.options.OnPeers(n)
	}
}

// Close disconnects every phone and stops serving. The saved identity stays.
func (b *Bridge) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	for c := range b.conns {
		c.Close()
	}
	b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
	if b.files != nil {
		b.files.Close()
	}
	b.listener.Close()
	for _, listener := range b.listeners {
		listener.Close()
	}
	// Keep the overlay alive long enough to deliver TCP FINs.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.server.DrainTCP(ctx)
	b.server.Close()
	releaseIdentity(b.lock)
}
