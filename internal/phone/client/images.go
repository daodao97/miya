package client

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/fsutil"
	phonetransfer "miya/internal/phone/transfer"
	"miya/internal/remote"
	"miya/internal/service/workspaces"
	"miya/internal/thumbnail"
)

func (m *Client) PreviewPath(source string) string {
	dir := m.CacheDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "miya-phone-previews", workspaces.CacheName(m.Link))
	}
	return filepath.Join(dir, "images", workspaces.CacheName(source)+".jpg")
}

func (m *Client) RetryImage(source string, conn *remote.Conn) {
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
				m.change()
			}
		})
	})
}

func (m *Client) keepSentImagePreviews(local, sources []string) map[string]bool {
	if len(local) != len(sources) || len(local) == 0 {
		return nil
	}
	if m.PreviewPaths == nil {
		m.PreviewPaths = map[string]string{}
	}
	retained := map[string]bool{}
	for i, source := range sources {
		if source == "" || m.PreviewPaths[source] != "" {
			continue
		}
		path, cached, gen := local[i], m.PreviewPath(source), m.Generation
		m.PreviewPaths[source], retained[path] = path, true
		go func() {
			data, err := os.ReadFile(path)
			if err != nil {
				return
			}
			data, err = thumbnail.Data(data)
			if err != nil || StorePhonePreview(cached, data) != nil {
				return
			}
			m.onMain(gen, func() {
				if m.PreviewPaths[source] == path {
					m.PreviewPaths[source] = cached
				}
				go os.Remove(path)
				m.change()
			})
		}()
	}
	return retained
}

func (m *Client) ReceiveImage(preview thumbnail.Image) {
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
			m.change()
		})
	}()
}

func (m *Client) ImagePath(source string) string {
	for _, p := range m.Outbox {
		for _, path := range p.Message.Images {
			if source == path {
				return path
			}
		}
	}
	if !strings.HasPrefix(source, "file://") && !filepath.IsAbs(source) {
		return source
	}
	if path, ok := m.PreviewPaths[source]; ok {
		return path
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
		})
	}()
	return ""
}
func StorePhonePreview(path string, data []byte) error {
	if len(data) == 0 || len(data) > thumbnail.MaxBytes {
		return errors.New(tr("Invalid image preview"))
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > thumbnail.Edge || config.Height > thumbnail.Edge {
		return errors.New(tr("Invalid image preview"))
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(path, func(f *os.File) error { _, err := f.Write(data); return err }); err != nil {
		return err
	}
	phonetransfer.PruneOldUploads(filepath.Dir(path))
	return nil
}
func (m *Client) RemoveImage(i int) {
	if i < 0 || i >= len(m.Images) {
		return
	}
	p := m.Images[i]
	m.Images = append(m.Images[:i:i], m.Images[i+1:]...)
	go os.Remove(p)
}
func (m *Client) clearImages() {
	for _, p := range m.Images {
		go os.Remove(p)
	}
	m.Images = nil
}

func (m *Client) AddImage(path string) bool {
	if len(m.Images) >= phonetransfer.MaxImages {
		m.SendErr = fmt.Sprintf(tr("Send up to %d images at a time"), phonetransfer.MaxImages)
		return false
	}
	m.Images = append(m.Images, path)
	return true
}
