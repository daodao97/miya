package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (m *mobileApp) previewPath(source string) string {
	dir := m.cacheDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "miya-phone-previews", cacheName(m.link))
	}
	return filepath.Join(dir, "images", cacheName(source)+".jpg")
}

// messageImage never tries to open a desktop path on the phone. Resolve its
// cached thumbnail off the UI thread, asking the paired desktop only once
// per connection when missing. Image sources in messages stay unchanged.
func (m *mobileApp) messageImage(source string) ui.ImageSource {
	if !strings.HasPrefix(source, "file://") && !filepath.IsAbs(source) {
		return cachedImage(source)
	}
	if path, ok := m.previewPaths[source]; ok {
		if path != "" {
			return cachedImage(path)
		}
		return nil
	}
	if m.previewPaths == nil {
		m.previewPaths = map[string]string{}
	}
	m.previewPaths[source] = ""
	path, gen := m.previewPath(source), m.generation
	go func() {
		_, err := os.Stat(path)
		m.onMain(gen, func() {
			if m.previewPaths == nil {
				m.previewPaths = map[string]string{}
			}
			if err == nil {
				m.previewPaths[source] = path
			} else if m.conn != nil {
				r := m.threadRequest("image", m.open)
				r.ImageSource = source
				m.sendPhoneAction(r)
			}
			invalidate()
		})
	}()
	return nil
}

func storePhonePreview(path string, data []byte) error {
	if len(data) == 0 || len(data) > maxPhonePreviewBytes {
		return errors.New("无效的图片预览")
	}
	img, err := ui.DecodeImage(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := writeFileAtomic(path, func(f *os.File) error { _, err := f.Write(data); return err }); err != nil {
		return err
	}
	imageMu.Lock()
	imageCache[path] = img
	imageMu.Unlock()
	pruneOldUploads(filepath.Dir(path))
	return nil
}

func (m *mobileApp) receiveImage(preview phoneImagePreview) {
	path, gen := m.previewPath(preview.Source), m.generation
	go func() {
		if storePhonePreview(path, preview.Data) != nil {
			return
		}
		m.onMain(gen, func() {
			if m.previewPaths == nil {
				m.previewPaths = map[string]string{}
			}
			m.previewPaths[preview.Source] = path
			invalidate()
		})
	}()
}
