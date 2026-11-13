package apns

import (
	"context"
	"testing"
	"time"
)

// waitGuard bounds a test's wait only so a hang fails with what never
// happened instead of stalling the run; it is not a speed assumption.
const waitGuard = time.Minute

// waitFor waits for a value from ch. what says what is waited for.
func waitFor[T any](t testing.TB, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitGuard):
		t.Fatalf("%s never happened (waited %v)", what, waitGuard)
		var zero T
		return zero
	}
}

// waitContext is canceled after waitGuard or when the test ends.
func waitContext(t testing.TB) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), waitGuard)
	t.Cleanup(cancel)
	return ctx
}
