package integration

import (
	"net/http"
)

type displayRoundTripFunc func(*http.Request) (*http.Response, error)

func (f displayRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
