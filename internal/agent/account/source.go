package account

import "context"

// Source is bound to one integration's current connection. Capture it on the
// workspace executor before starting a background read or confirmed reset.
type Source interface {
	Read(context.Context, Display) (Display, bool)
	ReadLimits(context.Context) (Limits, error)
	Reset(context.Context, ResetAttempt) (ResetExecution, error)
}
