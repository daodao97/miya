package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func attachmentPair(t *testing.T, wrap func(http.Handler) http.Handler) (*Conn, *Conn, *attachmentHub) {
	t.Helper()
	hub := new(attachmentHub)
	s, err := hub.newSession()
	if err != nil {
		t.Fatal(err)
	}
	handler := hub.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ordinary file")) }))
	if wrap != nil {
		handler = wrap(handler)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	a, b := net.Pipe()
	desk, phone := newConn(a, nil), newConn(b, nil)
	desk.localAttachments = s
	phone.dialPort = func(ctx context.Context, port uint16) (net.Conn, error) {
		if port != FilesPort {
			t.Errorf("wrong port: %d", port)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	if err := phone.EnableAttachments(s.token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { phone.Close(); desk.Close() })
	return desk, phone, hub
}

func exchangeRequest(t *testing.T, desk, phone *Conn, req Request) Request {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- phone.Send(req) }()
	desk.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got Request
	if err := desk.Receive(&got); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBinaryImageRoundTripAndSingleUse(t *testing.T) {
	desk, phone, hub := attachmentPair(t, nil)
	data := make([]byte, 1<<20)
	rand.Read(data)
	got := exchangeRequest(t, desk, phone, Request{Op: "send", Thread: 7, Text: "inspect", Images: []Image{{Name: "照片.jpg", Data: data}, {Name: "b.png", Data: []byte("second")}}})
	if !got.BinaryAttachments || got.Images[0].Name != "照片.jpg" || !bytes.Equal(got.Images[0].Data, data) || string(got.Images[1].Data) != "second" || got.Images[0].ID != "" {
		t.Fatal("binary images did not survive the control request")
	}
	if phone.Written() > 2048 {
		t.Fatalf("image bytes leaked into control stream: %d", phone.Written())
	}
	if !desk.localAttachments.peerEnabled.Load() {
		t.Fatal("phone did not negotiate thumbnail support")
	}
	desk.localAttachments.mu.Lock()
	left := len(desk.localAttachments.items)
	desk.localAttachments.mu.Unlock()
	if left != 0 {
		t.Fatal("consumed uploads retained")
	}
	id := strings.Repeat("a", 32)
	item, err := desk.localAttachments.reserve(id, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	desk.localAttachments.publish(id, item, []byte("raw"))
	if err := desk.receiveImages(&Request{Images: []Image{{ID: id}, {ID: id}}}); err == nil {
		t.Fatal("duplicate references accepted")
	}
	if err := desk.receiveImages(&Request{Images: []Image{{ID: id}}}); err != nil {
		t.Fatal("invalid batch partially consumed attachment", err)
	}
	if err := desk.receiveImages(&Request{Images: []Image{{ID: id}}}); err == nil {
		t.Fatal("upload replay accepted")
	}
	other, _ := hub.newSession()
	defer other.close()
	if err := other.takeUploads([]Image{{ID: id}}); err == nil {
		t.Fatal("another session accessed the upload")
	}
}

func TestBinaryPreviewAndLegacyFallback(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(map[bool]string{false: "old_phone", true: "new_phone"}[binary], func(t *testing.T) {
			desk, phone, _ := attachmentPair(t, nil)
			if binary {
				exchangeRequest(t, desk, phone, Request{Op: "image"})
			} else {
				phone.attachmentClient = nil
			}
			data := bytes.Repeat([]byte{0, 1, 2, 255}, 8192)
			done := make(chan error, 1)
			go func() { done <- desk.SendImage("file:///photo.jpg", data) }()
			var f Frame
			if err := phone.Receive(&f); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var preview struct {
				Source string `json:"source"`
				Data   []byte `json:"data"`
			}
			if err := json.Unmarshal(f.Data, &preview); err != nil {
				t.Fatal(err)
			}
			if preview.Source != "file:///photo.jpg" {
				t.Fatal("thumbnail lost source identity")
			}
			if binary {
				if f.Attachment == "" || len(preview.Data) != 0 || len(f.Data) > 100 {
					t.Fatal("binary thumbnail was embedded in JSON")
				}
				got, err := phone.ReadAttachment(context.Background(), f.Attachment)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatal("thumbnail transfer", err)
				}
				if _, err := phone.ReadAttachment(context.Background(), f.Attachment); err == nil {
					t.Fatal("thumbnail replay accepted")
				}
			} else if f.Attachment != "" || !bytes.Equal(preview.Data, data) {
				t.Fatal("old phone lost inline thumbnail")
			}
		})
	}
	t.Run("old_desktop", func(t *testing.T) {
		a, b := net.Pipe()
		desk, phone := newConn(a, nil), newConn(b, nil)
		defer desk.Close()
		defer phone.Close()
		if err := phone.EnableAttachments(""); err != nil {
			t.Fatal(err)
		}
		got := exchangeRequest(t, desk, phone, Request{Op: "send", Images: []Image{{Name: "a", Data: []byte("inline")}}})
		if got.BinaryAttachments || string(got.Images[0].Data) != "inline" {
			t.Fatal("old desktop did not get legacy upload")
		}
	})
}

func TestAttachmentHTTPValidationAndExpiry(t *testing.T) {
	hub := new(attachmentHub)
	s, _ := hub.newSession()
	defer s.close()
	other, _ := hub.newSession()
	defer other.close()
	handler := hub.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("file")) }))
	call := func(method, id, token string, body []byte, length int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, attachmentPrefix+id, bytes.NewReader(body))
		r.ContentLength = length
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	id := strings.Repeat("a", 32)
	if w := call("PUT", id, "invalid", []byte("abc"), 3); w.Code != 404 {
		t.Fatal("invalid token accepted")
	}
	if w := call("PUT", id, s.token, []byte("abc"), MaxImageBytes+1); w.Code != 413 {
		t.Fatal("oversize upload accepted")
	}
	if w := call("PUT", id, s.token, []byte("a"), 3); w.Code != 400 {
		t.Fatal("truncated upload accepted")
	}
	if len(s.items) != 0 {
		t.Fatal("failed upload leaked budget")
	}
	if w := call("PUT", id, s.token, []byte("abc"), 3); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if w := call("GET", id, s.token, nil, 0); w.Code != 404 {
		t.Fatal("upload exposed as download")
	}
	if w := call("GET", id, other.token, nil, 0); w.Code != 404 {
		t.Fatal("cross-session access accepted")
	}
	if err := other.takeUploads([]Image{{ID: id}}); err == nil {
		t.Fatal("cross-session control reference accepted")
	}
	if w := call("PUT", id, s.token, []byte("xyz"), 3); w.Code != 409 {
		t.Fatal("existing upload replaced")
	}
	s.prune(time.Now().Add(attachmentTTL + time.Second))
	if len(s.items) != 0 {
		t.Fatal("expired upload retained")
	}
	for i := 0; i < MaxImages; i++ {
		key, _ := randomAttachmentID(16)
		item, err := s.reserve(key, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		s.publish(key, item, []byte{1})
	}
	if _, err := s.reserve(id, 1, false); err == nil {
		t.Fatal("upload count limit missing")
	}
	s.close()
	if w := call("PUT", id, s.token, []byte("abc"), 3); w.Code != 404 {
		t.Fatal("closed control session still authorized")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/project/file", nil))
	if w.Body.String() != "file" {
		t.Fatal("attachment routing broke files")
	}
}

func TestSlowBinaryUploadDoesNotBlockControl(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	desk, phone, _ := attachmentPair(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PUT" {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	defer close(release)
	done := make(chan error, 1)
	go func() {
		done <- phone.Send(Request{Op: "send", Images: []Image{{Name: "large.jpg", Data: make([]byte, 1<<20)}}})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("upload never started")
	}
	got := exchangeRequest(t, desk, phone, Request{Op: "stop", Thread: 7})
	if got.Op != "stop" {
		t.Fatal("control message was blocked by upload")
	}
	// Cancel the transfer while it is blocked. The caller must return promptly,
	// without waiting for attachment expiry or cleanup HTTP requests.
	phone.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed upload succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close did not cancel upload")
	}
}

func TestSlowBinaryPreviewDoesNotBlockControl(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	desk, phone, _ := attachmentPair(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	exchangeRequest(t, desk, phone, Request{Op: "image"})
	send := make(chan error, 1)
	go func() { send <- desk.SendImage("file:///photo.jpg", make([]byte, 32<<10)) }()
	var f Frame
	if err := phone.Receive(&f); err != nil {
		t.Fatal(err)
	}
	if err := <-send; err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := phone.ReadAttachment(context.Background(), f.Attachment); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	go func() { send <- desk.Send(Frame{Type: "responded", Thread: 7}) }()
	phone.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := phone.Receive(&f); err != nil || f.Type != "responded" {
		t.Fatal("thumbnail blocked control reader", err)
	}
	if err := <-send; err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentSizeBudgetsAndCancellation(t *testing.T) {
	hub := new(attachmentHub)
	s, _ := hub.newSession()
	defer s.close()
	for range 2 {
		id, _ := randomAttachmentID(16)
		if _, err := s.reserve(id, MaxImageBytes, false); err != nil {
			t.Fatal(err)
		}
	}
	id, _ := randomAttachmentID(16)
	if _, err := s.reserve(id, 1, false); err == nil {
		t.Fatal("upload byte budget missing")
	}
	id, _ = randomAttachmentID(16)
	item, err := s.reserve(id, MaxPreviewBytes, true)
	if err != nil {
		t.Fatal("uploads starved thumbnails", err)
	}
	s.publish(id, item, make([]byte, MaxPreviewBytes))
	if err := s.takeUploads([]Image{{ID: id}}); err == nil {
		t.Fatal("thumbnail accepted as upload")
	}
}

// Measures actual HTTP attachment upload plus the real control codec, with no
// relay, encryption or image decoding. Compare with the identical legacy path.
func BenchmarkAttachmentUpload(b *testing.B) {
	for _, size := range []int{32 << 10, 1 << 20} {
		for _, binary := range []bool{false, true} {
			name := "legacy"
			if binary {
				name = "binary"
			}
			b.Run(name+"/"+stringSize(size), func(b *testing.B) {
				hub := new(attachmentHub)
				s, _ := hub.newSession()
				defer s.close()
				server := httptest.NewServer(hub.handler(nil))
				defer server.Close()
				a, z := net.Pipe()
				desk, phone := newConn(a, nil), newConn(z, nil)
				defer desk.Close()
				defer phone.Close()
				desk.localAttachments = s
				phone.dialPort = func(ctx context.Context, _ uint16) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
				}
				if binary {
					phone.EnableAttachments(s.token)
				}
				raw := make([]byte, size)
				rand.Read(raw)
				errCh := make(chan error, 1)
				go func() {
					for {
						var r Request
						if err := desk.Receive(&r); err != nil {
							errCh <- err
							return
						}
						if !bytes.Equal(r.Images[0].Data, raw) {
							errCh <- io.ErrUnexpectedEOF
							return
						}
					}
				}()
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for b.Loop() {
					if err := phone.Send(Request{Op: "send", Images: []Image{{Name: "photo.jpg", Data: raw}}}); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				phone.Close()
				<-errCh
			})
		}
	}
}
func stringSize(n int) string {
	if n == 1<<20 {
		return "1MiB"
	}
	return "32KiB"
}
