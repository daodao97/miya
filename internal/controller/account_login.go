package controller

import (
	"context"
	"errors"
	"strings"

	"miya/internal/agent/integrations"
	"miya/internal/agent/login"
)

// AccountLoginTarget captures the target on the executor. Every native
// operation, including status, stays on that target even if the window moves.
func (c *Controller) AccountLoginTarget() func(login.Request) (login.State, error) {
	if cl := c.Remote; cl != nil {
		return func(r login.Request) (login.State, error) {
			var state login.State
			err := cl.Call("accountLogin", r, &state)
			if err != nil && strings.Contains(err.Error(), `unknown operation "accountLogin"`) {
				err = errors.New("Sign-in is not supported by this service. Update Miya on the target computer and reconnect.")
			}
			return state, err
		}
	}
	if c.logins == nil {
		ctx := c.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		c.logins = integrations.Logins(ctx)
	}
	integrations.SetLoginClient(c.logins, c.Client)
	m := c.logins
	return func(r login.Request) (login.State, error) { return m.Do(r) }
}
