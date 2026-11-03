package runtime

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ProbeHTTP labels verified local listeners without following redirects or
// treating non-HTTP ports as exited processes. The caller proves ownership.
func ProbeHTTP(ctx context.Context, endpoints []Endpoint) ([]Endpoint, error) {
	transport := &http.Transport{DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	out := append([]Endpoint{}, endpoints...)
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(out)) {
		workers.Go(func() {
			for i := range jobs {
				out[i].Protocol = "tcp"
				hosts := []string{out[i].Host}
				if out[i].Host == "" {
					hosts = []string{"127.0.0.1", "::1"}
				}
				for _, host := range hosts {
					if !LocalHost(host) {
						continue
					}
					req, err := http.NewRequestWithContext(ctx, http.MethodHead, "http://"+net.JoinHostPort(host, strconv.Itoa(out[i].Port))+"/", nil)
					if err != nil {
						continue
					}
					req.Host = "localhost:" + strconv.Itoa(out[i].Port)
					response, err := client.Do(req)
					if err == nil {
						response.Body.Close()
						out[i].Host = host
						out[i].Protocol = "http"
						break
					}
				}
			}
		})
	}
	for i := range out {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, nil
}
