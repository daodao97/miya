package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"miya/internal/desktop"
)

// desktopClient speaks the Mac's control API over the paired, encrypted
// files channel. The bearer token stays in this process; nothing is served on
// a loopback port and no WebView sees it.
type desktopClient struct {
	token     string
	transport *http.Transport
	http      *http.Client
}

// desktopHTTPError keeps the status so the page can tell an ended lease (410),
// missing permission (403), a busy desktop (409/429) and other failures apart.
type desktopHTTPError struct {
	Status  int
	Message string
}

func (e *desktopHTTPError) Error() string { return e.Message }

func desktopStatus(err error) int {
	var e *desktopHTTPError
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

const desktopClientMaxBody = 2 << 20

func newDesktopClient(dial func(context.Context) (net.Conn, error), token string) *desktopClient {
	transport := &http.Transport{MaxIdleConns: 4, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			return dial(ctx)
		},
	}
	return &desktopClient{token: token, transport: transport, http: &http.Client{Transport: transport}}
}

func (c *desktopClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	data, _, err := c.doHeader(ctx, method, path, body)
	return data, err
}

func (c *desktopClient) doHeader(ctx context.Context, method, path string, body any) ([]byte, http.Header, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://desktop"+desktopControlPrefix+path, reader)
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, errors.New(tr("Unable to connect to your Mac. Check that it is online and retry."))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, desktopClientMaxBody+1))
	if response.StatusCode/100 != 2 {
		message := strings.TrimSpace(string(data))
		if r := []rune(message); len(r) > 300 {
			message = string(r[:300])
		}
		return nil, response.Header, &desktopHTTPError{Status: response.StatusCode, Message: firstNonEmpty(message, fmt.Sprintf(tr("Mac returned an error (%d)"), response.StatusCode))}
	}
	if err != nil {
		return nil, nil, err
	}
	if len(data) > desktopClientMaxBody {
		return nil, nil, errors.New(tr("Invalid or oversized frame"))
	}
	return data, response.Header, nil
}

type desktopSession struct {
	WindowOnly bool            `json:"windowOnly"`
	Window     *desktop.Window `json:"window"`
	Pick       bool            `json:"pick"`
	Apps       bool            `json:"apps"`
}

func (c *desktopClient) applications(ctx context.Context) ([]desktop.Application, error) {
	var apps []desktop.Application
	data, err := c.do(ctx, http.MethodGet, "/apps", nil)
	if err == nil {
		err = json.Unmarshal(data, &apps)
	}
	return apps, err
}

func (c *desktopClient) launchApplication(ctx context.Context, id string) (desktop.Application, error) {
	var app desktop.Application
	data, err := c.do(ctx, http.MethodPost, "/launch", struct {
		ID string `json:"id"`
	}{id})
	if err == nil {
		err = json.Unmarshal(data, &app)
	}
	return app, err
}

type desktopActions struct {
	Window  desktop.Window   `json:"window"`
	Actions []desktop.Action `json:"actions"`
	Pending bool             `json:"pending"`
}

func (c *desktopClient) session(ctx context.Context) (desktopSession, error) {
	var s desktopSession
	data, err := c.do(ctx, http.MethodGet, "/session", nil)
	if err == nil {
		err = json.Unmarshal(data, &s)
	}
	return s, err
}

func (c *desktopClient) windows(ctx context.Context) ([]desktop.Window, error) {
	var windows []desktop.Window
	data, err := c.do(ctx, http.MethodGet, "/windows", nil)
	if err == nil {
		err = json.Unmarshal(data, &windows)
	}
	return windows, err
}

func (c *desktopClient) selectWindow(ctx context.Context, w desktop.Window) error {
	_, err := c.do(ctx, http.MethodPost, "/select", struct {
		ID  uint32 `json:"id"`
		PID int    `json:"pid"`
	}{w.ID, w.PID})
	return err
}

// thumbnail previews one listed window; only menu leases provide it.
func (c *desktopClient) thumbnail(ctx context.Context, w desktop.Window) ([]byte, error) {
	return c.do(ctx, http.MethodGet, fmt.Sprintf("/thumbnail?id=%d&pid=%d", w.ID, w.PID), nil)
}

func (c *desktopClient) start(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/start", struct{}{})
	return err
}

// frame also reports whether the Mac currently shows its lock screen.
func (c *desktopClient) frame(ctx context.Context) ([]byte, bool, error) {
	data, header, err := c.doHeader(ctx, http.MethodGet, "/frame", nil)
	if err == nil && len(data) == 0 {
		err = errors.New(tr("Invalid or oversized frame"))
	}
	return data, header.Get(desktopLockedHeader) == "1", err
}

// unlock types password into the Mac's lock screen and waits for the result.
func (c *desktopClient) unlock(ctx context.Context, password string) error {
	_, err := c.do(ctx, http.MethodPost, "/unlock", struct {
		Password string `json:"password"`
	}{password})
	return err
}

func (c *desktopClient) input(ctx context.Context, in desktop.Input) error {
	if err := in.Validate(); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodPost, "/input", in)
	return err
}

func (c *desktopClient) actions(ctx context.Context) (desktopActions, error) {
	var a desktopActions
	data, err := c.do(ctx, http.MethodGet, "/actions", nil)
	if err == nil {
		err = json.Unmarshal(data, &a)
	}
	return a, err
}

func (c *desktopClient) pressAction(ctx context.Context, id string, w desktop.Window) error {
	_, err := c.do(ctx, http.MethodPost, "/action", struct {
		ID        string `json:"id"`
		WindowID  uint32 `json:"windowId"`
		WindowPID int    `json:"windowPid"`
	}{id, w.ID, w.PID})
	return err
}

// stop revokes the lease. It uses its own short deadline so it still runs
// when the page's context has already been cancelled.
func (c *desktopClient) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = c.do(ctx, http.MethodPost, "/stop", struct{}{})
	c.transport.CloseIdleConnections()
}
