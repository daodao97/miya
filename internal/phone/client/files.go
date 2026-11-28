package client

import (
	"path/filepath"
	"strings"

	"miya/internal/chat/presentation"
)

func (m *Client) messageFileTarget(relative string) (project, file string, ok bool) {
	cwd := m.ComposerProject()
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", "", false
	}
	absolute := filepath.Clean(filepath.Join(cwd, relative))
	for _, candidate := range m.State.Projects {
		root := filepath.Clean(candidate.Path)
		if !filepath.IsAbs(root) || filepath.Dir(root) == root && root != filepath.Clean(cwd) {
			continue
		}
		rel, err := filepath.Rel(root, absolute)
		if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if !ok || len(root) > len(project) {
			project, file, ok = root, filepath.ToSlash(rel), true
		}
	}
	return
}

// MessageFileTarget resolves a conversation link in the paired desktop's
// namespace. An empty project identifies a single file outside project browsing.
func (m *Client) MessageFileTarget(link string) (project, file string, ok bool) {
	cwd := m.ComposerProject()
	if relative, ok := presentation.LocalFileLink(cwd, link); ok {
		if project, file, ok := m.messageFileTarget(relative); ok {
			return project, file, true
		}
	}
	file, ok = presentation.RemoteFileLink(cwd, link)
	return "", file, ok
}
