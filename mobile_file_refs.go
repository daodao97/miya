package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"miya/internal/remote"
)

type mobileFileReferences struct {
	project, query string
	conn           *remote.Conn
	cancel         context.CancelFunc
	choices        []composerChoice
	loading        bool
	supported      bool
	err            string
}

func (m *mobileApp) clearFileReferences() {
	if m.fileRefs != nil && m.fileRefs.cancel != nil {
		m.fileRefs.cancel()
	}
	m.fileRefs = nil
}

func (m *mobileApp) searchFileReferences(project, query string) *mobileFileReferences {
	if s := m.fileRefs; s != nil && s.project == project && s.query == query && s.conn == m.conn && s.supported == m.state.FileReferences {
		return s
	}
	m.clearFileReferences()
	s := &mobileFileReferences{project: project, query: query, conn: m.conn, supported: m.state.FileReferences}
	m.fileRefs = s
	if !m.state.FileReferences {
		s.err = tr("Update the desktop app to reference project files")
		return s
	}
	if !m.connected || m.conn == nil {
		s.err = tr("Connect to your desktop to search project files")
		return s
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	s.cancel, s.loading = cancel, true
	gen := m.generation
	go func() {
		defer cancel()
		timer := time.NewTimer(120 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		choices, err := fetchPhoneFileReferences(ctx, s.conn.DialFiles, project, query)
		m.onMain(gen, func() {
			if m.fileRefs != s {
				return
			}
			s.loading, s.choices = false, choices
			if err != nil {
				s.err = tr("Unable to search project files. Try again later.")
			}
		})
	}()
	return s
}

func fetchPhoneFileReferences(ctx context.Context, dial func(context.Context) (net.Conn, error), project, query string) ([]composerChoice, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://desktop/"+projectFileID(project)+"/.miya-references?q="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("project file search unavailable")
	}
	var choices []composerChoice
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&choices)
	return choices, err
}
