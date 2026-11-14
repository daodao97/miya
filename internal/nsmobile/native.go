package nsmobile

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

// scopeSecrets adapts NSGo's asynchronous secure store for the existing phone
// cache contract. Calls run on a worker; all NSGo requests run on the engine.
// Unsupported platforms return their error and never fall back to cleartext.
type scopeSecrets struct {
	cx        *core.Scope
	legacyIOS bool
}

func (m *App) setupNative(cx *core.Scope) {
	if m.Store == nil {
		m.Store = scopeSecrets{cx: cx, legacyIOS: cx.Env().OS() == core.OSIOS}
	}
	m.setupDesktopAuthentication()
	m.setupNetwork(cx)
	if !m.Loaded {
		cx.Post(func() { go m.LoadSaved(core.ErrSecretNotFound) })
	}
}

func (s scopeSecrets) read(service, key string) (string, error) {
	type result struct {
		value string
		err   error
	}
	ch := make(chan result, 1)
	s.cx.Post(func() { s.cx.ReadSecret(service, key, func(v string, e error) { ch <- result{v, e} }) })
	select {
	case r := <-ch:
		if r.err != nil {
			return "", r.err
		}
		return r.value, nil
	case <-time.After(10 * time.Second):
		return "", errors.New("secure storage timed out")
	}
}

// Keep the original NSGo base64 format under Miya. MyGo iOS uses raw
// bytes under the bundle identifier; copy only missing items, and retire
// the source only after its replacement is durable. Android's older Keystore
// file format is private to MyGo and cannot be read through NSGo's API.
func (s scopeSecrets) Get(key string) ([]byte, error) {
	value, err := s.read("Miya", key)
	if err == nil {
		return base64.StdEncoding.DecodeString(value)
	}
	if !errors.Is(err, core.ErrSecretNotFound) || !s.legacyIOS {
		return nil, err
	}
	value, err = s.read("com.daodao.miya", key)
	if err != nil {
		return nil, err
	}
	data := []byte(value)
	if err := s.Set(key, data); err != nil {
		return nil, err
	}
	if err := s.delete("com.daodao.miya", key); err != nil && !errors.Is(err, core.ErrSecretNotFound) {
		return nil, fmt.Errorf("retire legacy credential: %w", err)
	}
	return data, nil
}
func (s scopeSecrets) Set(key string, value []byte) error {
	ch := make(chan error, 1)
	encoded := base64.StdEncoding.EncodeToString(value)
	s.cx.Post(func() { s.cx.StoreSecret("Miya", key, encoded, func(err error) { ch <- err }) })
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		return errors.New("secure storage timed out")
	}
}
func (s scopeSecrets) Delete(key string) error {
	err := s.delete("Miya", key)
	if err != nil && !errors.Is(err, core.ErrSecretNotFound) {
		return err
	}
	if s.legacyIOS {
		err = s.delete("com.daodao.miya", key)
		if err != nil && !errors.Is(err, core.ErrSecretNotFound) {
			return err
		}
	}
	return nil
}
func (s scopeSecrets) delete(service, key string) error {
	ch := make(chan error, 1)
	s.cx.Post(func() { s.cx.DeleteSecret(service, key, func(err error) { ch <- err }) })
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		return errors.New("secure storage timed out")
	}
}
