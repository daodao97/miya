package remote

import (
	"context"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const RemoteFileEndpoint = "/.miya-remote-file"

// AuthorizeRemoteFile binds file requests to the current paired control session.
func (c *Conn) AuthorizeRemoteFile(r *http.Request) {
	if r.URL.Path == RemoteFileEndpoint && c.attachmentClient != nil {
		r.Header.Set("Authorization", "Bearer "+c.attachmentClient.token)
	}
}

func (h *attachmentHub) serveRemoteFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !validAttachmentID(token, 32) {
		http.NotFound(w, r)
		return
	}
	h.mu.Lock()
	session := h.sessions[token]
	h.mu.Unlock()
	if session == nil || session.ctx.Err() != nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Only viewing and downloading are supported", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	name := query.Get("path")
	if !filepath.IsAbs(name) || strings.ContainsRune(name, 0) {
		http.Error(w, "An absolute desktop file path is required", http.StatusBadRequest)
		return
	}
	before, err := os.Stat(name)
	if err != nil || !before.Mode().IsRegular() {
		http.Error(w, "Desktop file unavailable", http.StatusNotFound)
		return
	}
	file, err := os.OpenFile(name, os.O_RDONLY|remoteFileOpenFlags, 0)
	if err != nil {
		http.Error(w, "Desktop file unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "Desktop file unavailable", http.StatusNotFound)
		return
	}
	control := http.NewResponseController(w)
	stop := context.AfterFunc(session.ctx, func() { _ = control.SetWriteDeadline(time.Now()) })
	defer stop()
	disposition := "inline"
	if query.Get("download") == "1" {
		disposition = "attachment"
	}
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": info.Name()}); value != "" {
		w.Header().Set("Content-Disposition", value)
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
