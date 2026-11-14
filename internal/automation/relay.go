package automation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tailscale/tailcat"
)

const relayBaseURL = "https://tailcat-webhook.daodao973597.workers.dev"
const relayBodyLimit = 64<<10 + 1 // The relay may add a final newline.

type relayEndpoint struct {
	Scope, ID, Revision string
	Enabled             bool
}

func (t relayEndpoint) key() string { return t.Scope + "/" + t.ID }
func (t relayEndpoint) fingerprint() string {
	h := sha256.Sum256([]byte(t.Revision))
	return hex.EncodeToString(h[:])
}

// relayRegistration is service-private state. In particular the manage key
// never travels in an Automation or a View sent to a window or phone.
type relayRegistration struct {
	URL         string `json:"url"`
	ManageKey   string `json:"manageKey"`
	Fingerprint string `json:"fingerprint"`
}

type relayStatus struct {
	URL, Error string
}

type relayListener struct {
	listener net.Listener
	cancel   context.CancelFunc
	address  string
	port     int
	refresh  time.Time
}

func (l relayListener) close() {
	l.cancel()
	_ = l.listener.Close()
}

// relayReceiver provides the TCP transport for Miya’s shared inbox.
// Routing messages to tasks happens after reception, on the host thread.
type relayReceiver interface {
	Listen(context.Context) (net.Listener, string, int, error)
	Close() error
}

type tailcatReceiver struct {
	tailcat.Server
	nextPort int
}

func (s *tailcatReceiver) Listen(ctx context.Context) (net.Listener, string, int, error) {
	// Never reuse a retired endpoint’s port on this overlay: a paused or
	// not-yet-revoked registration must not reach a replacement endpoint.
	if s.nextPort >= 65535 {
		return nil, "", 0, errors.New("The Tailcat webhook receiver has no unused ports")
	}
	s.nextPort++
	ln, err := s.Server.Listen(ctx, "tcp", ":"+strconv.Itoa(s.nextPort))
	if err != nil {
		return nil, "", 0, errors.New("Unable to start the Tailcat webhook receiver")
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, "", 0, err
	}
	n, _ := strconv.Atoi(port)
	return ln, string(s.TailcatAddr()), n, nil
}

// relay owns its network and credentials on a worker goroutine. Snapshots
// arrive from the host thread; only public status and wake events go back.
type relay struct {
	ctx      context.Context
	cancel   context.CancelFunc
	updates  chan []relayEndpoint
	path     string
	baseURL  string
	client   *http.Client
	receiver relayReceiver
	changed  func(map[string]relayStatus)
	deliver  func(string)

	registrations map[string]relayRegistration
	listeners     map[string]relayListener
	statuses      map[string]relayStatus
	retry         map[string]time.Time
	loadErr       error
	dirty         bool
	workers       sync.WaitGroup
	slots         chan struct{}
}

func newRelay(path string, changed func(map[string]relayStatus), deliver func(string)) *relay {
	ctx, cancel := context.WithCancel(context.Background())
	return &relay{ctx: ctx, cancel: cancel, updates: make(chan []relayEndpoint, 1), path: path,
		baseURL: relayBaseURL, client: &http.Client{Timeout: 12 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		receiver: &tailcatReceiver{Server: tailcat.Server{Logf: func(string, ...any) {}}},
		changed:  changed, deliver: deliver, registrations: map[string]relayRegistration{},
		listeners: map[string]relayListener{}, statuses: map[string]relayStatus{}, retry: map[string]time.Time{}, slots: make(chan struct{}, 16)}
}

func (r *relay) update(targets []relayEndpoint) {
	select {
	case <-r.updates:
	default:
	}
	r.updates <- targets
}

func (r *relay) run() {
	defer func() {
		for _, ln := range r.listeners {
			ln.close()
		}
		r.workers.Wait()
		r.receiver.Close()
	}()
	r.loadErr = r.load()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	var targets []relayEndpoint
	for {
		select {
		case <-r.ctx.Done():
			return
		case targets = <-r.updates:
		case <-ticker.C:
		}
		if r.ctx.Err() != nil {
			return
		}
		r.reconcile(targets, time.Now())
	}
}

func (r *relay) load() error {
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("Unable to read saved webhook credentials")
	}
	if json.Unmarshal(data, &r.registrations) != nil || r.registrations == nil {
		return errors.New("Saved webhook credentials are invalid")
	}
	if err := os.Chmod(r.path, 0600); err != nil {
		return errors.New("Unable to protect saved webhook credentials")
	}
	return nil
}

func (r *relay) save() error {
	data, err := json.MarshalIndent(r.registrations, "", "  ")
	if err != nil {
		return err
	}
	// The automation store has already created this directory. CreateTemp
	// gives the credentials a private mode even if an old file was public.
	f, err := os.CreateTemp(filepath.Dir(r.path), ".webhooks-*")
	if err != nil {
		return errors.New("Unable to save webhook credentials")
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil || os.Rename(f.Name(), r.path) != nil {
		return errors.New("Unable to save webhook credentials")
	}
	r.dirty = false
	return nil
}

// reconcile keeps paused registrations and revokes retired endpoints.
// This also cleans up the earlier per-task registrations during migration.
func (r *relay) reconcile(targets []relayEndpoint, now time.Time) {
	wanted := make(map[string]relayEndpoint, len(targets))
	for _, t := range targets {
		wanted[t.key()] = t
	}
	for key, ln := range r.listeners {
		t, ok := wanted[key]
		if !ok || !t.Enabled || r.registrations[key].Fingerprint != t.fingerprint() {
			ln.close()
			delete(r.listeners, key)
		}
	}
	if r.loadErr != nil {
		states := map[string]relayStatus{}
		for key := range wanted {
			states[key] = relayStatus{Error: r.loadErr.Error()}
		}
		r.changed(states)
		return
	}
	for key, reg := range r.registrations {
		t, ok := wanted[key]
		if ok && reg.Fingerprint == t.fingerprint() {
			continue
		}
		if now.Before(r.retry[key]) {
			continue
		}
		_, err := r.request(http.MethodDelete, "/receiver", reg.ManageKey, nil)
		if err != nil && !invalidManagementKey(err) {
			r.failed(key, reg.URL, err, now)
			continue
		}
		delete(r.registrations, key)
		delete(r.statuses, key)
		delete(r.retry, key)
		r.dirty = true
	}
	for _, t := range targets {
		key := t.key()
		reg := r.registrations[key]
		if reg.ManageKey != "" && reg.Fingerprint != t.fingerprint() {
			continue // Wait for revocation before replacing the registration.
		}
		if !t.Enabled {
			r.statuses[key] = relayStatus{URL: reg.URL}
			continue
		}
		if now.Before(r.retry[key]) {
			continue
		}
		listener, listening := r.listeners[key]
		if listening && now.Before(listener.refresh) {
			continue
		}
		if !listening {
			ctx, cancel := context.WithTimeout(r.ctx, 12*time.Second)
			ln, addr, port, err := r.receiver.Listen(ctx)
			cancel()
			if err != nil {
				r.failed(key, reg.URL, err, now)
				continue
			}
			// Accept before registering: a caller can use the URL as soon as
			// /register or /receiver completes.
			ctx, cancel = context.WithCancel(r.ctx)
			listener = relayListener{listener: ln, cancel: cancel, address: addr, port: port}
			r.listeners[key] = listener
			r.workers.Go(func() { r.accept(ctx, ln) })
		}
		body := map[string]any{"address": listener.address, "port": listener.port}
		var reply relayResponse
		var err error
		if reg.ManageKey != "" {
			_, err = r.request(http.MethodPut, "/receiver", reg.ManageKey, body)
			if invalidManagementKey(err) {
				reg = relayRegistration{} // Expired or revoked at the relay.
				err = nil
			}
		}
		if err == nil && reg.ManageKey == "" {
			reply, err = r.request(http.MethodPost, "/register", "", body)
			if err == nil {
				if reply.ManageKey == "" || !strings.HasPrefix(reply.URL, r.baseURL+"/push/tcw_") {
					err = errors.New("The webhook relay returned invalid credentials")
				} else {
					reg = relayRegistration{URL: reply.URL, ManageKey: reply.ManageKey, Fingerprint: t.fingerprint()}
					r.registrations[key], r.dirty = reg, true
				}
			}
		}
		if err != nil {
			r.listeners[key].close()
			delete(r.listeners, key)
			r.failed(key, reg.URL, err, now)
			continue
		}
		delete(r.retry, key)
		// Registrations with no deliveries expire. Check periodically so an
		// expired registration can be replaced without restarting Miya.
		listener.refresh = now.Add(time.Hour)
		r.listeners[key] = listener
		r.statuses[key] = relayStatus{URL: reg.URL}
	}
	var saveErr error
	if r.dirty {
		saveErr = r.save()
	}
	states := make(map[string]relayStatus, len(targets))
	for key := range wanted {
		states[key] = r.statuses[key]
		if saveErr != nil {
			states[key] = relayStatus{Error: saveErr.Error()}
		}
	}
	r.changed(states)
}

func (r *relay) failed(key, url string, err error, now time.Time) {
	r.statuses[key] = relayStatus{URL: url, Error: err.Error()}
	delay := time.Minute
	var apiErr *relayAPIError
	if errors.As(err, &apiErr) {
		delay = max(delay, apiErr.retryAfter)
	}
	r.retry[key] = now.Add(delay)
}

type relayResponse struct {
	OK        bool   `json:"ok"`
	URL       string `json:"webhook_url"`
	ManageKey string `json:"manage_key"`
	Error     string `json:"error"`
}

type relayAPIError struct {
	status     int
	code       string
	retryAfter time.Duration
}

func (e *relayAPIError) Error() string {
	return fmt.Sprintf("Webhook relay: %s (HTTP %d)", e.code, e.status)
}

func invalidManagementKey(err error) bool {
	var e *relayAPIError
	return errors.As(err, &e) && e.status == http.StatusUnauthorized && e.code == "invalid_manage_key"
}

func (r *relay) request(method, path, manageKey string, body any) (relayResponse, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return relayResponse{}, err
	}
	req, err := http.NewRequestWithContext(r.ctx, method, r.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return relayResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if manageKey != "" {
		req.Header.Set("Authorization", "Bearer "+manageKey)
	}
	res, err := r.client.Do(req)
	if err != nil {
		// HTTP errors can contain credential-bearing URLs. Keep those out
		// of views and logs.
		return relayResponse{}, errors.New("Unable to reach the webhook relay")
	}
	defer res.Body.Close()
	var reply relayResponse
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&reply)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		code := reply.Error
		if code == "" {
			code = http.StatusText(res.StatusCode)
		}
		delay := time.Duration(0)
		if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil {
			delay = time.Duration(min(max(seconds, 0), 86400)) * time.Second
		} else if date, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil {
			delay = time.Until(date)
		}
		return relayResponse{}, &relayAPIError{res.StatusCode, code, delay}
	}
	if decodeErr != nil || !reply.OK {
		return relayResponse{}, errors.New("Invalid response from the webhook relay")
	}
	return reply, nil
}

func (r *relay) accept(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case r.slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		r.workers.Go(func() {
			defer func() { <-r.slots; conn.Close() }()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			data, err := io.ReadAll(io.LimitReader(conn, relayBodyLimit+1))
			if err != nil || len(data) > relayBodyLimit || !utf8.Valid(data) || strings.TrimSpace(string(data)) == "" || ctx.Err() != nil {
				return
			}
			r.deliver(string(data))
		})
	}
}

const globalRelayKey = "miya/inbox"

func (rn *Runner) syncRelay() {
	if !rn.listen {
		return
	}
	if rn.relay == nil {
		var worker *relay
		worker = newRelay(rn.Path+".webhooks.json", func(states map[string]relayStatus) {
			rn.host.Dispatch(func() {
				if rn.relay == worker && rn.listen && !reflect.DeepEqual(rn.relayStates, states) {
					rn.relayStates = states
					rn.host.Changed()
				}
			})
		}, func(data string) {
			rn.host.Dispatch(func() {
				if rn.relay == worker && rn.listen && !rn.Store.WebhookDisabled {
					rn.ReceiveWebhook(data)
				}
			})
		})
		rn.relay = worker
		go worker.run()
	}
	// One registration belongs to Miya, independently of automation count,
	// names, triggers, pauses and deletions. Old per-task registrations are
	// revoked by reconcile; they are never silently redirected elsewhere.
	rn.relay.update([]relayEndpoint{{Scope: "miya", ID: "inbox", Revision: "1", Enabled: !rn.Store.WebhookDisabled}})
}
