package mobile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/fsutil"
	phonetransfer "miya/internal/phone/transfer"
	"miya/internal/remote"
	"miya/internal/thumbnail"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
	"miya/internal/workspaces"
)

func (m *App) PreviewPath(source string) string {
	dir := m.CacheDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "miya-phone-previews", workspaces.CacheName(m.Link))
	}
	return filepath.Join(dir, "images", workspaces.CacheName(source)+".jpg")
}

// A full preview queue or an expired download must not leave an empty cache
// reservation forever. Retry with backoff, only on the connection that failed.
func (m *App) RetryImage(source string, conn *remote.Conn) {
	path, requested := m.PreviewPaths[source]
	if source == "" || !requested || m.Conn != conn || !m.Connected || path != "" {
		return
	}
	if m.PreviewRetries == nil {
		m.PreviewRetries = map[string]int{}
	}
	attempt := m.PreviewRetries[source]
	if attempt >= 3 {
		return
	}
	m.PreviewRetries[source] = attempt + 1
	gen := m.Generation
	time.AfterFunc(time.Second<<attempt, func() {
		m.onMain(gen, func() {
			if m.Conn == conn && m.Connected && m.PreviewPaths[source] == "" {
				delete(m.PreviewPaths, source)
				uiruntime.Invalidate()
			}
		})
	})
}

// messageImage never tries to open a desktop path on the phone. Resolve its
// cached thumbnail off the UI thread, with bounded retries when a transfer
// fails. Image sources in messages stay unchanged.
func (m *App) MessageImage(source string) ui.ImageSource {
	if m.NewSend != nil {
		for _, path := range m.NewSend.Message.Images {
			if source == path {
				return widgets.CachedImage(path)
			}
		}
	}
	if !strings.HasPrefix(source, "file://") && !filepath.IsAbs(source) {
		return widgets.CachedImage(source)
	}
	if path, ok := m.PreviewPaths[source]; ok {
		if path != "" {
			return widgets.CachedImage(path)
		}
		return nil
	}
	if m.PreviewPaths == nil {
		m.PreviewPaths = map[string]string{}
	}
	m.PreviewPaths[source] = ""
	path, gen := m.PreviewPath(source), m.Generation
	go func() {
		_, err := os.Stat(path)
		m.onMain(gen, func() {
			if m.PreviewPaths == nil {
				m.PreviewPaths = map[string]string{}
			}
			if err == nil {
				m.PreviewPaths[source] = path
			} else if m.Conn != nil {
				r := m.ThreadRequest("image", m.Open)
				r.ImageSource = source
				m.sendPhoneAction(r)
			}
			uiruntime.Invalidate()
		})
	}()
	return nil
}

func StorePhonePreview(path string, data []byte) error {
	if len(data) == 0 || len(data) > thumbnail.MaxBytes {
		return errors.New(tr("Invalid image preview"))
	}
	img, err := ui.DecodeImage(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(path, func(f *os.File) error { _, err := f.Write(data); return err }); err != nil {
		return err
	}
	widgets.ImageMu.Lock()
	widgets.ImageCache[path] = img
	widgets.ImageMu.Unlock()
	phonetransfer.PruneOldUploads(filepath.Dir(path))
	return nil
}

func (m *App) ReceiveImage(preview thumbnail.Image) {
	path, gen := m.PreviewPath(preview.Source), m.Generation
	conn := m.Conn
	go func() {
		if StorePhonePreview(path, preview.Data) != nil {
			m.onMain(gen, func() { m.RetryImage(preview.Source, conn) })
			return
		}
		m.onMain(gen, func() {
			if m.PreviewPaths == nil {
				m.PreviewPaths = map[string]string{}
			}
			m.PreviewPaths[preview.Source] = path
			uiruntime.Invalidate()
		})
	}()
}
