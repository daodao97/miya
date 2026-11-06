package server

import (
	"context"
	"log"
	"net/http"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"miya/internal/remote"
	svcproto "miya/internal/service/protocol"
)

// Host is what Pairing needs from the controller that answers phones.
type Host interface {
	// Serve answers one phone connection until it ends.
	Serve(ctx context.Context, conn *remote.Conn)
	// Files serves the project files phones browse.
	Files() http.Handler
	// Dispatch runs f on the controller's thread.
	Dispatch(f func())
	// RunOnMain runs f on the UI thread; Invalidate draws a new frame.
	RunOnMain(f func())
	Invalidate()
}

// Pairing owns the desktop's bridge to phones: its link, QR code and the
// phones connected. Its fields belong to the main thread.
type Pairing struct {
	host Host
	// Service, Terminal and SaveLink are set by a persistent background
	// service: it then also serves windows and terminals over the bridge,
	// retries a failed start, and saves the pairing link where a server
	// deployment finds it. SaveLink returns where it saved the link.
	Service    func(ctx context.Context, conn *remote.Conn)
	Terminal   func(ctx context.Context, conn *remote.Conn)
	SaveLink   func(link string) (path string, err error)
	bridge     *remote.Bridge
	link       string
	qr         [][]bool
	peers      int
	busy       bool
	enabled    bool
	message    string
	generation int
}

// NewPairing is an idle bridge for host's phones.
func NewPairing(host Host) *Pairing { return &Pairing{host: host} }

// Close disconnects phones as the app quits, leaving the saved setting.
func (p *Pairing) Close() {
	p.generation++
	if b := p.bridge; b != nil {
		p.bridge = nil
		b.Close()
	}
}

// Start turns the bridge on and keeps it on at the next launch.
func (p *Pairing) Start() {
	if p.bridge != nil || p.busy {
		return
	}
	dir := Dir()
	if dir == "" {
		p.message = "Unable to determine data directory"
		return
	}
	p.enabled = true
	PersistEnabled(true)
	p.busy, p.message = true, ""
	p.generation++
	gen := p.generation
	host := p.host
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		options := remote.Options{
			StateDir: dir,
			Serve:    host.Serve,
			Files:    host.Files(),
			OnPeers: func(n int) {
				host.RunOnMain(func() {
					if p.generation == gen {
						p.peers = n
						host.Invalidate()
					}
				})
			},
		}
		if p.Service != nil {
			options.Service = p.Service
			options.Terminal = p.Terminal
		}
		b, err := remote.Start(ctx, options)
		var link string
		var qr [][]bool
		if err == nil {
			link = b.Link()
			if code, qerr := qrcode.New(link, qrcode.Medium); qerr == nil {
				qr = code.Bitmap()
			}
		}
		host.RunOnMain(func() {
			if p.generation != gen {
				if b != nil {
					go b.Close()
				}
				return
			}
			p.busy = false
			if err != nil {
				p.message = "Unable to enable mobile connections: " + err.Error()
				if p.Service != nil {
					log.Printf("miya: Tailcat startup failed: %v; retrying", err)
					time.AfterFunc(5*time.Second, func() {
						host.Dispatch(func() {
							if p.enabled && p.generation == gen {
								p.Start()
							}
						})
					})
				}
			} else {
				p.bridge, p.link, p.qr = b, link, qr
				if p.Service != nil && p.SaveLink != nil {
					if path, err := p.SaveLink(link); err != nil {
						p.message = err.Error()
						log.Printf("miya: cannot save pairing link: %v", err)
					} else {
						log.Printf("miya: Tailcat ready; pairing link saved in %s", path)
					}
				}
			}
			host.Invalidate()
		})
	}()
}

// Stop disconnects phones and stays off at the next launch. The identity is
// kept, so turning it on again shows the same QR code.
func (p *Pairing) Stop() {
	p.generation++
	p.enabled = false
	PersistEnabled(false)
	if b := p.bridge; b != nil {
		go b.Close()
	}
	p.bridge, p.link, p.qr, p.peers, p.busy, p.message = nil, "", nil, 0, false, ""
}

// Reset revokes the current QR code: phones holding it can no longer
// connect, and a new one is shown.
func (p *Pairing) Reset() {
	if p.busy {
		return
	}
	p.generation++
	b := p.bridge
	p.bridge, p.link, p.qr, p.peers, p.busy, p.message = nil, "", nil, 0, true, ""
	dir := Dir()
	host := p.host
	go func() {
		if b != nil {
			b.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		err := remote.ForgetIdentity(ctx, dir)
		host.RunOnMain(func() {
			p.busy = false
			if err != nil {
				p.message = "Unable to reset connection: " + err.Error()
				host.Invalidate()
				return
			}
			p.Start()
		})
	}()
}

// Status is what the settings show of the connection; a nil Pairing is off.
func (p *Pairing) Status() svcproto.PhoneStatus {
	if p == nil {
		return svcproto.PhoneStatus{}
	}
	s := svcproto.PhoneStatus{Enabled: p.enabled, Busy: p.busy, Peers: p.peers, Message: p.message}
	if p.bridge != nil {
		s.Link = p.link
	}
	return s
}

// Connected reports whether a phone is connected.
func (p *Pairing) Connected() bool { return p != nil && p.peers > 0 }

// Do runs a settings action: start, stop or reset.
func (p *Pairing) Do(action string) {
	if p == nil {
		return
	}
	switch action {
	case "start":
		p.Start()
	case "stop":
		p.Stop()
	case "reset":
		p.Reset()
	}
}
