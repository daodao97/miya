package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/appdir"
)

// loadReferenceSource persists a public data-instance ID, independent of pairing.
// Link the complete temporary file so concurrent initializers cannot replace it.
func LoadReferenceSource(dir string) (string, error) {
	path := filepath.Join(dir, "reference-source")
	read := func() (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		id := strings.TrimSpace(string(data))
		if !SourceIDPattern.MatchString(id) {
			return "", errors.New("Invalid conversation reference source file")
		}
		return id, nil
	}
	if id, err := read(); !os.IsNotExist(err) {
		return id, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(bytes[:])
	f, err := os.CreateTemp(dir, ".reference-source-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(id + "\n"); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return "", err
	}
	return read()
}

func ReferenceSourceForService() (string, string) {
	id, err := LoadReferenceSource(appdir.Dir())
	if err != nil {
		return "", "Unable to initialize conversation reference source: " + err.Error()
	}
	return id, ""
}
