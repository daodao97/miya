//go:build !darwin || ios

package selfupdate

import "context"

func (m *Manager) ReopenWindows(context.Context) error { return nil }
