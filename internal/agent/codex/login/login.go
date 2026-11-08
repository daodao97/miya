package login

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"sync"
	"time"

	"miya/internal/agent/cli"
	native "miya/internal/agent/codex/native"
	"miya/internal/agent/login"
)

type Provider struct {
	Binary, Home string
	Client       *native.Client
}

func (p Provider) client(ctx context.Context) (*native.Client, string, error) {
	binary, home := p.Binary, p.Home
	var err error
	if binary == "" {
		binary, err = native.Binary()
	}
	if err != nil {
		binary, err = cli.FindBinary("codex")
	}
	if err != nil {
		return nil, "", errors.New("Codex not found. Install the official CLI first.")
	}
	if home == "" {
		home, err = cli.ConfigDir("codex")
	}
	if err != nil {
		return nil, "", err
	}
	dir, err := os.MkdirTemp("", "miya-codex-login-")
	if err != nil {
		return nil, "", err
	}
	cl, err := native.StartWithEnv(ctx, binary, dir, cli.CommandEnv("codex", home))
	if err != nil {
		os.RemoveAll(dir)
		return nil, "", errors.New("Unable to start native Codex sign-in. Update the official CLI and try again.")
	}
	return cl, dir, nil
}

func (p Provider) Check(ctx context.Context) (login.Identity, error) {
	cl := p.Client
	if cl == nil {
		owned, dir, err := p.client(ctx)
		if err != nil {
			return login.Identity{}, err
		}
		defer os.RemoveAll(dir)
		defer closeOwned(owned)
		cl = owned
	}
	var result struct {
		Account *struct{ Type, Email, PlanType string }
	}
	if cl.Call(ctx, "account/read", map[string]bool{"refreshToken": false}, &result) != nil {
		return login.Identity{}, errors.New("Unable to check Codex sign-in")
	}
	if result.Account == nil {
		return login.Identity{}, nil
	}
	a := result.Account
	return login.Identity{LoggedIn: true, Method: a.Type, Email: a.Email, Plan: a.PlanType}, nil
}

func (p Provider) Start(ctx context.Context, method string) (login.Flow, login.Challenge, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cl := p.Client
	f := &flow{client: cl}
	if cl == nil {
		owned, dir, err := p.client(ctx)
		if err != nil {
			return nil, login.Challenge{}, err
		}
		cl, f.client, f.dir, f.owned = owned, owned, dir, true
	}
	f.events, f.unwatch = cl.WatchNotifications("account/login/completed")
	const typ = "chatgptDeviceCode"
	var result struct{ Type, LoginID, VerificationURL, UserCode string }
	if cl.Call(ctx, "account/login/start", map[string]any{"type": typ}, &result) != nil {
		f.Close()
		return nil, login.Challenge{}, errors.New("Unable to start native Codex device sign-in. Enable device code login in ChatGPT security settings and update the official CLI, then try again.")
	}
	f.id = result.LoginID
	link := result.VerificationURL
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Host != "auth.openai.com" && u.Host != "chatgpt.com") || f.id == "" || result.Type != typ {
		f.Close()
		return nil, login.Challenge{}, errors.New("Codex returned an unrecognized sign-in link")
	}
	if result.UserCode == "" {
		f.Close()
		return nil, login.Challenge{}, errors.New("Codex returned no device code")
	}
	return f, login.Challenge{URL: link, UserCode: result.UserCode}, nil
}

type flow struct {
	client  *native.Client
	dir, id string
	once    sync.Once
	owned   bool
	events  <-chan native.Frame
	unwatch func()
}

func (f *flow) Submit(context.Context, string) error {
	return errors.New("Enter the device code on the official authorization page")
}

func (f *flow) Wait(ctx context.Context) error {
	for {
		select {
		case frame, ok := <-f.events:
			if !ok {
				return errors.New("Native Codex sign-in connection closed")
			}
			if frame.Method != "account/login/completed" {
				continue
			}
			var result struct {
				LoginID string
				Success bool
			}
			if json.Unmarshal(frame.Params, &result) != nil || result.LoginID != f.id {
				continue
			}
			if !result.Success {
				return errors.New("Native Codex sign-in failed. Generate a new link and try again.")
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-f.client.Done():
			return errors.New("Native Codex sign-in connection closed")
		}
	}
}

func (f *flow) Close() {
	f.once.Do(func() {
		if f.id != "" {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = f.client.Call(ctx, "account/login/cancel", map[string]string{"loginId": f.id}, nil)
			cancel()
		}
		if f.unwatch != nil {
			f.unwatch()
		}
		if f.owned {
			closeOwned(f.client)
			os.RemoveAll(f.dir)
		}
	})
}

func closeOwned(client *native.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.CloseAndWait(ctx)
}
