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
	server   *tailcat.Server
	listener net.Listener
	files    *http.Server
	lock     *os.File
	ctx      context.Context
	cancel   context.CancelFunc
	options  Options

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
	if options.StateDir != "" {
		if err := saveIdentity(options.StateDir, b.server); err != nil {
			b.Close()
			return nil, err
		}
	}
	if options.Files != nil {
		files, err := b.server.Listen(ctx, "tcp", fmt.Sprintf(":%d", FilesPort))
		if err != nil {
			b.Close()
			return nil, err
		}
		b.files = &http.Server{Handler: options.Files, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
		go b.files.Serve(files)
	}
	go b.accept()
	return b, nil
}

// Link is the miya:// link the QR code shows.
func (b *Bridge) Link() string { return Link(b.server.TailcatAddr()) }

func (b *Bridge) accept() {
	for {
		nc, err := b.listener.Accept()
		if err != nil {
			return
		}
		conn := newConn(nc, nil)
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
			if b.options.Serve != nil {
				b.options.Serve(b.ctx, conn)
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
	// Keep the overlay alive long enough to deliver TCP FINs.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.server.DrainTCP(ctx)
	b.server.Close()
	releaseIdentity(b.lock)
}
