package nsmobile

import (
	"errors"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

const legacySecretsComponent = "miya-legacy-secrets"

type legacySecretsView struct{}

func (legacySecretsView) View() any           { return "dev.oneo.nsgo.MiyaLegacySecrets" }
func (legacySecretsView) SetProp(string, any) {}
func (legacySecretsView) Command(string, any) {}
func (legacySecretsView) Close()              {}

type legacyMigration struct {
	mu   sync.Mutex
	done chan struct{}
	err  error
}

func (m *legacyMigration) finish(success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.done:
		if !success {
			return
		}
	default:
		close(m.done)
	}
	if success {
		m.err = nil
	} else {
		m.err = errors.New(tr("Unable to import the saved Android connection. Restart Miya to retry."))
	}
}

func (m *legacyMigration) wait() error {
	if m == nil {
		return nil
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-m.done:
	case <-timer.C:
		m.finish(false)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

func androidMigrationView(m *legacyMigration) core.Node {
	if m == nil {
		return nil
	}
	return core.Native{Component: legacySecretsComponent, Props: map[string]any{"start": true},
		Style:   core.Style{Position: core.Absolute, Width: core.Pt(1), Height: core.Pt(1), Opacity: core.Alpha(0)},
		OnError: func(error) { m.finish(false) },
		OnEvent: func(name string, data any) {
			if name != "onMigrationComplete" {
				return
			}
			if args, ok := data.([]any); ok && len(args) == 1 {
				if success, ok := args[0].(bool); ok {
					m.finish(success)
				}
			}
		},
	}
}
