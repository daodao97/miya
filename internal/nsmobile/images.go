package nsmobile

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	phonetransfer "miya/internal/phone/transfer"

	"github.com/nsgo-dev/nsgo/core"
)

func imageURI(path string) string {
	if strings.HasPrefix(path, "data:") || strings.HasPrefix(path, "file:") || strings.HasPrefix(path, "https:") || strings.HasPrefix(path, "http:") {
		return path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
func (m *App) addPhotos() {
	remaining := phonetransfer.MaxImages - len(m.Images)
	if remaining <= 0 {
		m.SendErr = fmt.Sprintf(tr("Send up to %d images at a time"), phonetransfer.MaxImages)
		return
	}
	epochLink, epochOpen, epochProject := m.Link, m.Open, m.NewProject
	epoch := m.ComposerEpoch()
	m.cx.PickImages(core.PickOptions{Multiple: true, Limit: remaining}, func(files []core.PickedFile) {
		// A picker may outlive navigation or pairing; never attach to a new owner.
		if m.Link != epochLink || m.Open != epochOpen || m.NewProject != epochProject || m.ComposerEpoch() != epoch {
			return
		}
		cache := m.cx.CacheDir()
		if cache == "" {
			cache = os.TempDir()
		}
		dir := filepath.Join(cache, "miya-phone-uploads")
		// Encoding and filesystem writes stay off the engine. Picked bytes have
		// already been bounded and normalized by NSGo's system photo picker.
		gen := m.Generation
		go func() {
			var paths []string
			_ = os.MkdirAll(dir, 0700)
			phonetransfer.PruneOldUploads(dir)
			for _, file := range files {
				if err := os.MkdirAll(dir, 0700); err != nil {
					continue
				}
				suffix := filepath.Ext(file.Name)
				if suffix == "" {
					suffix = ".jpg"
				}
				f, err := os.CreateTemp(dir, "photo-*"+suffix)
				if err != nil {
					continue
				}
				_, err = f.Write(file.Data)
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					_ = os.Remove(f.Name())
					continue
				}
				paths = append(paths, f.Name())
			}
			m.cx.Post(func() {
				if m.Generation != gen || m.Link != epochLink || m.Open != epochOpen || m.NewProject != epochProject || m.ComposerEpoch() != epoch {
					for _, p := range paths {
						_ = os.Remove(p)
					}
					return
				}
				for _, p := range paths {
					if !m.AddImage(p) {
						go os.Remove(p)
					}
				}
				m.change()
			})
		}()
	})
}
