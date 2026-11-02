package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Binary attachments use independent HTTP streams inside the same encrypted
// overlay as the control connection. Nothing is exposed on the public network.
const (
	MaxImages        = 6
	MaxImageBytes    = 10 << 20
	MaxImageTotal    = 20 << 20
	MaxPreviewBytes  = 2 << 20
	attachmentPrefix = "/.miya-attachments/v1/"
	attachmentTTL    = 2 * time.Minute
	maxPreviewTotal  = 8 * MaxPreviewBytes
)

var errAttachment = errors.New("remote: attachment unavailable or invalid")

type attachment struct {
	data     []byte
	size     int
	download bool
	active   bool
	expires  time.Time
}

type attachmentHub struct {
	mu       sync.Mutex
	sessions map[string]*attachmentSession
}

type attachmentSession struct {
	hub         *attachmentHub
	token       string
	ctx         context.Context
	cancel      context.CancelFunc
	peerEnabled atomic.Bool
	mu          sync.Mutex
	closed      bool
	items       map[string]*attachment
}

func randomAttachmentID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validAttachmentID(s string, n int) bool {
	if len(s) != n*2 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

func (h *attachmentHub) newSession() (*attachmentSession, error) {
	token, err := randomAttachmentID(32)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &attachmentSession{hub: h, token: token, ctx: ctx, cancel: cancel, items: make(map[string]*attachment)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = make(map[string]*attachmentSession)
	}
	h.sessions[token] = s
	return s, nil
}

func (s *attachmentSession) close() {
	s.cancel()
	s.mu.Lock()
	s.closed = true
	clear(s.items)
	s.mu.Unlock()
	s.hub.mu.Lock()
	delete(s.hub.sessions, s.token)
	s.hub.mu.Unlock()
}

func (s *attachmentSession) prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, item := range s.items {
		if !item.active && !now.Before(item.expires) {
			delete(s.items, id)
		}
	}
}

func (h *attachmentHub) reap(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			h.mu.Lock()
			sessions := make([]*attachmentSession, 0, len(h.sessions))
			for _, s := range h.sessions {
				sessions = append(sessions, s)
			}
			h.mu.Unlock()
			for _, s := range sessions {
				s.prune(now)
			}
		case <-ctx.Done():
			return
		}
	}
}

// reserve holds the byte/count budget even while an HTTP body is in flight.
// Downloads have a separate budget, so an upload cannot starve thumbnails.
func (s *attachmentSession) reserve(id string, size int, download bool) (*attachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.items[id] != nil {
		return nil, errAttachment
	}
	count, total := 0, 0
	for _, item := range s.items {
		if item.download == download {
			count++
			total += item.size
		}
	}
	maxCount, maxTotal, maxSize := MaxImages, MaxImageTotal, MaxImageBytes
	if download {
		maxCount, maxTotal, maxSize = 8, maxPreviewTotal, MaxPreviewBytes
	}
	if size <= 0 || size > maxSize || count >= maxCount || size > maxTotal-total {
		return nil, errAttachment
	}
	item := &attachment{size: size, download: download, active: true, expires: time.Now().Add(attachmentTTL)}
	s.items[id] = item
	return item, nil
}

func (s *attachmentSession) remove(id string, item *attachment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[id] == item {
		delete(s.items, id)
	}
}

func (s *attachmentSession) publish(id string, item *attachment, data []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.items[id] != item {
		return false
	}
	item.data, item.active = data, false
	return true
}

func (h *attachmentHub) handler(files http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, attachmentPrefix) {
			if files != nil {
				files.ServeHTTP(w, r)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		id := strings.TrimPrefix(r.URL.Path, attachmentPrefix)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !validAttachmentID(id, 16) || !validAttachmentID(token, 32) {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		s := h.sessions[token]
		h.mu.Unlock()
		if s == nil {
			http.NotFound(w, r)
			return
		}
		s.prune(time.Now())
		// Bound slow bodies without imposing a timeout on ordinary file downloads.
		control := http.NewResponseController(w)
		_ = control.SetReadDeadline(time.Now().Add(attachmentTTL))
		_ = control.SetWriteDeadline(time.Now().Add(attachmentTTL))
		switch r.Method {
		case http.MethodPut:
			if r.ContentLength <= 0 || r.ContentLength > MaxImageBytes {
				http.Error(w, "invalid attachment size", http.StatusRequestEntityTooLarge)
				return
			}
			item, err := s.reserve(id, int(r.ContentLength), false)
			if err != nil {
				http.Error(w, "attachment limit reached", http.StatusConflict)
				return
			}
			defer s.removeIfActive(id, item)
			stop := context.AfterFunc(s.ctx, func() { _ = control.SetReadDeadline(time.Now()) })
			defer stop()
			data := make([]byte, int(r.ContentLength))
			if _, err = io.ReadFull(r.Body, data); err != nil {
				http.Error(w, "incomplete attachment", http.StatusBadRequest)
				return
			}
			var extra [1]byte
			if n, err := r.Body.Read(extra[:]); n != 0 || err != io.EOF {
				http.Error(w, "invalid attachment length", http.StatusBadRequest)
				return
			}
			if !s.publish(id, item, data) {
				http.Error(w, "connection closed", http.StatusGone)
				return
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			s.mu.Lock()
			item := s.items[id]
			if item == nil || !item.download || item.active {
				s.mu.Unlock()
				http.NotFound(w, r)
				return
			}
			item.active = true
			data := item.data
			s.mu.Unlock()
			defer s.remove(id, item)
			stop := context.AfterFunc(s.ctx, func() { _ = control.SetWriteDeadline(time.Now()) })
			defer stop()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = io.Copy(w, bytes.NewReader(data))
		case http.MethodDelete:
			s.mu.Lock()
			if item := s.items[id]; item != nil && !item.active {
				delete(s.items, id)
			}
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *attachmentSession) removeIfActive(id string, item *attachment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[id] == item && item.active {
		delete(s.items, id)
	}
}

// takeUploads validates the whole list before consuming any ID. A reference
// cannot cross connections, be replayed, or select a thumbnail as an upload.
func (s *attachmentSession) takeUploads(images []Image) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(images) > MaxImages {
		return errAttachment
	}
	seen := make(map[string]bool, len(images))
	total := 0
	for _, img := range images {
		total += len(img.Data)
		if img.ID == "" {
			if len(img.Data) > MaxImageBytes {
				return errAttachment
			}
			continue
		}
		item := s.items[img.ID]
		if len(img.Data) != 0 || seen[img.ID] || item == nil || item.active || item.download || !time.Now().Before(item.expires) {
			return errAttachment
		}
		seen[img.ID] = true
		total += item.size
	}
	if total > MaxImageTotal {
		return errAttachment
	}
	for i := range images {
		if id := images[i].ID; id != "" {
			images[i].Data = s.items[id].data
			images[i].ID = ""
			delete(s.items, id)
		}
	}
	return nil
}

type attachmentClient struct {
	token     string
	http      *http.Client
	downloads chan struct{}
}

// EnableAttachments runs after hello and before publishing Conn to senders.
// An empty token means an older desktop, so ordinary inline JSON stays active.
func (c *Conn) EnableAttachments(token string) error {
	if token == "" {
		return nil
	}
	if !validAttachmentID(token, 32) || c.dialPort == nil {
		return errAttachment
	}
	transport := &http.Transport{MaxIdleConns: 2, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) { return c.DialFiles(ctx) },
	}
	c.attachmentClient = &attachmentClient{token: token, http: &http.Client{Transport: transport, Timeout: attachmentTTL}, downloads: make(chan struct{}, 8)}
	return nil
}

// AttachmentToken binds the optional transport to this exact control session.
func (c *Conn) AttachmentToken() string {
	if c.localAttachments != nil {
		return c.localAttachments.token
	}
	return ""
}

func (c *Conn) attachmentRequest(ctx context.Context, method, id string, body []byte) (*http.Response, error) {
	client := c.attachmentClient
	if client == nil || !validAttachmentID(id, 16) {
		return nil, errAttachment
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	req, err := http.NewRequestWithContext(ctx, method, "http://desktop"+attachmentPrefix+id, bytes.NewReader(body))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+client.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := client.http.Do(req)
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	res.Body = &attachmentBody{ReadCloser: res.Body, finish: func() { stop(); cancel() }}
	return res, nil
}

type attachmentBody struct {
	io.ReadCloser
	finish func()
}

func (b *attachmentBody) Close() error { err := b.ReadCloser.Close(); b.finish(); return err }

func (c *Conn) sendRequest(req Request) error {
	if c.attachmentClient == nil {
		return c.sendJSON(req)
	}
	req.BinaryAttachments = true
	if len(req.Images) == 0 {
		return c.sendJSON(req)
	}
	if len(req.Images) > MaxImages {
		return errAttachment
	}
	total := 0
	for _, img := range req.Images {
		if len(img.Data) == 0 || len(img.Data) > MaxImageBytes || img.ID != "" {
			return errAttachment
		}
		total += len(img.Data)
	}
	if total > MaxImageTotal {
		return errAttachment
	}
	images := make([]Image, 0, len(req.Images))
	ctx, cancel := context.WithTimeout(c.ctx, attachmentTTL)
	defer cancel()
	defer func() {
		// PUTs not consumed by a control request are bounded and expire. DELETE
		// frees them promptly on a failed batch or failed control write.
		if len(images) == 0 {
			return
		}
		pending := images
		go func() {
			ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
			defer cancel()
			for _, img := range pending {
				res, err := c.attachmentRequest(ctx, http.MethodDelete, img.ID, nil)
				if err == nil {
					res.Body.Close()
				}
			}
		}()
	}()
	for _, img := range req.Images {
		id, err := randomAttachmentID(16)
		if err != nil {
			return err
		}
		images = append(images, Image{Name: img.Name, ID: id})
		res, err := c.attachmentRequest(ctx, http.MethodPut, id, img.Data)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			return fmt.Errorf("remote: attachment upload refused (%d)", res.StatusCode)
		}
	}
	req.Images = images
	err := c.sendJSON(req)
	if err == nil {
		images = nil
	} // Receive consumes them, or the session expiry does.
	return err
}

func (c *Conn) receiveImages(req *Request) error {
	if c.localAttachments != nil {
		if req.BinaryAttachments {
			c.localAttachments.peerEnabled.Store(true)
		}
		return c.localAttachments.takeUploads(req.Images)
	}
	for _, img := range req.Images {
		if img.ID != "" {
			return errAttachment
		}
	}
	return nil
}

// SendImage retains the legacy payload for old phones. The source has already
// been authorized against the conversation by the caller, before storing bytes.
func (c *Conn) SendImage(source string, data []byte) error {
	if len(data) == 0 || len(data) > MaxPreviewBytes {
		return errAttachment
	}
	s := c.localAttachments
	if s == nil || !s.peerEnabled.Load() {
		payload, err := json.Marshal(struct {
			Source string `json:"source"`
			Data   []byte `json:"data"`
		}{source, data})
		if err != nil {
			return err
		}
		return c.sendJSON(Frame{Type: "image", Data: payload})
	}
	id, err := randomAttachmentID(16)
	if err != nil {
		return err
	}
	s.prune(time.Now())
	item, err := s.reserve(id, len(data), true)
	if err != nil {
		return err
	}
	if !s.publish(id, item, data) {
		return errAttachment
	}
	payload, _ := json.Marshal(struct {
		Source string `json:"source"`
	}{source})
	if err = c.sendJSON(Frame{Type: "image", Data: payload, Attachment: id}); err != nil {
		s.remove(id, item)
	}
	return err
}

// ReadAttachment is called off the control reader; slow thumbnails must not
// delay thread deltas, approval responses or heartbeats on that connection.
func (c *Conn) ReadAttachment(ctx context.Context, id string) ([]byte, error) {
	if c.attachmentClient == nil {
		return nil, errAttachment
	}
	select {
	case c.attachmentClient.downloads <- struct{}{}:
		defer func() { <-c.attachmentClient.downloads }()
	default:
		return nil, errAttachment
	}
	res, err := c.attachmentRequest(ctx, http.MethodGet, id, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.ContentLength <= 0 || res.ContentLength > MaxPreviewBytes {
		return nil, errAttachment
	}
	data := make([]byte, int(res.ContentLength))
	if _, err = io.ReadFull(res.Body, data); err != nil {
		return nil, err
	}
	return data, nil
}
