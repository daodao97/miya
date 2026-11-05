package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/appdir"
	"miya/internal/model"
)

// loadReferenceSource persists a public data-instance ID, independent of pairing.
// Link the complete temporary file so concurrent initializers cannot replace it.
func loadReferenceSource(dir string) (string, error) {
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

func referenceSourceForService() (string, string) {
	id, err := loadReferenceSource(appdir.Dir())
	if err != nil {
		return "", "Unable to initialize conversation reference source: " + err.Error()
	}
	return id, ""
}

func MessageParts(text string) []MessagePart {
	var out []MessagePart
	end := 0
	for _, m := range composerMentions(text) {
		if m.Start > end {
			out = append(out, MessagePart{Type: "text", Text: text[end:m.Start]})
		}
		out = append(out, m.Part)
		end = m.End
	}
	if end < len(text) {
		out = append(out, MessagePart{Type: "text", Text: text[end:]})
	}
	return out
}

func PartsText(parts []MessagePart) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "text":
			b.WriteString(p.Text)
		case "thread_ref":
			if p.Ref == nil || !p.Ref.Valid() {
				return "", errors.New("Invalid conversation reference identity")
			}
			b.WriteString(model.ReferenceText(*p.Ref, p.TitleSnapshot))
		case "agent_ref":
			tag := agentReferenceText(p.Agent, p.TitleSnapshot)
			if tag == "" {
				return "", errors.New("Invalid agent reference identity")
			}
			b.WriteString(tag)
		default:
			return "", fmt.Errorf("Unknown message content type %q", p.Type)
		}
	}
	return b.String(), nil
}
