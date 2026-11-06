package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

type MobileFileReferences struct {
	Project, Query string
	conn           *remote.Conn
	Cancel         context.CancelFunc
	Choices        []model.ComposerChoice
	Loading        bool
	Supported      bool
	err            string
}

func (m *App) clearFileReferences() {
	if m.FileRefs != nil && m.FileRefs.Cancel != nil {
		m.FileRefs.Cancel()
	}
	m.FileRefs = nil
}

func (m *App) SearchFileReferences(project, query string) *MobileFileReferences {
	if s := m.FileRefs; s != nil && s.Project == project && s.Query == query && s.conn == m.Conn && s.Supported == m.State.FileReferences {
		return s
	}
	m.clearFileReferences()
	s := &MobileFileReferences{Project: project, Query: query, conn: m.Conn, Supported: m.State.FileReferences}
	m.FileRefs = s
	if !m.State.FileReferences {
		s.err = tr("Update the desktop app to reference project files")
		return s
	}
	if !m.Connected || m.Conn == nil {
		s.err = tr("Connect to your desktop to search project files")
		return s
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	s.Cancel, s.Loading = cancel, true
	gen := m.Generation
	go func() {
		defer cancel()
		timer := time.NewTimer(120 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		choices, err := FetchPhoneFileReferences(ctx, s.conn.DialFiles, project, query)
		m.onMain(gen, func() {
			if m.FileRefs != s {
				return
			}
			s.Loading, s.Choices = false, choices
			if err != nil {
				s.err = tr("Unable to search project files. Try again later.")
			}
		})
	}()
	return s
}

func FetchPhoneFileReferences(ctx context.Context, dial func(context.Context) (net.Conn, error), project, query string) ([]model.ComposerChoice, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://desktop/"+phoneproto.ProjectFileID(project)+"/.miya-references?q="+url.QueryEscape(query), nil)
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
	var choices []model.ComposerChoice
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&choices)
	return choices, err
}
