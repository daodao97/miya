package nsmobile

import (
	"encoding/base64"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

// scopeSecrets adapts NSGo's asynchronous secure store for the existing phone
// cache contract. Calls run on a worker; all NSGo requests run on the engine.
// Unsupported platforms return their error and never fall back to cleartext.
type scopeSecrets struct{ cx *core.Scope }

func (s scopeSecrets) Get(key string) ([]byte, error) {
	type result struct {
		value string
		err   error
	}
	ch := make(chan result, 1)
	s.cx.Post(func() { s.cx.ReadSecret("Miya", key, func(v string, e error) { ch <- result{v, e} }) })
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return base64.StdEncoding.DecodeString(r.value)
	case <-time.After(10 * time.Second):
		return nil, core.ErrPlatformUnsupported
	}
}
func (s scopeSecrets) Set(key string, value []byte) error {
	ch := make(chan error, 1)
	encoded := base64.StdEncoding.EncodeToString(value)
	s.cx.Post(func() { s.cx.StoreSecret("Miya", key, encoded, func(err error) { ch <- err }) })
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		return core.ErrPlatformUnsupported
	}
}
func (s scopeSecrets) Delete(key string) error {
	ch := make(chan error, 1)
	s.cx.Post(func() { s.cx.DeleteSecret("Miya", key, func(err error) { ch <- err }) })
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		return core.ErrPlatformUnsupported
	}
}
