package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"miya/internal/agent/account"
	"miya/internal/agent/cli"
	"miya/internal/agent/quota"
)

type codexWindow struct {
	Used  *float64 `json:"usedPercent"`
	Mins  int64    `json:"windowDurationMins"`
	Reset int64    `json:"resetsAt"`
}

type codexLimits struct {
	Plan      string       `json:"planType"`
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
}

type codexRPC struct {
	scanner *bufio.Scanner
	enc     *json.Encoder
	id      int
	home    string
}

func readCodex(ctx context.Context, binary, home string) (quota.Reading, error) {
	r := quota.Reading{Agent: "codex", Home: home}
	err := withCodex(ctx, binary, home, func(rpc *codexRPC) error {
		var err error
		r, err = rpc.read()
		return err
	})
	return r, err
}

func withCodex(ctx context.Context, binary, home string, run func(*codexRPC) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	cmd.Env, cmd.Dir, cmd.Stderr = cli.CommandEnv("codex", home, binary), os.TempDir(), io.Discard
	cmd.WaitDelay = time.Second
	cli.IsolateReader(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return errors.New("Unable to start the Codex usage reader")
	}
	defer func() { in.Close(); cancel(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	rpc := &codexRPC{scanner: scanner, enc: json.NewEncoder(in), home: home}
	if err := rpc.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "miya_usage", "version": "1.0"}}, nil); err != nil {
		return err
	}
	if err := rpc.enc.Encode(map[string]string{"method": "initialized"}); err != nil {
		return err
	}
	return run(rpc)
}

func (rpc *codexRPC) call(method string, params any, result any) error {
	rpc.id++
	if err := rpc.enc.Encode(map[string]any{"id": rpc.id, "method": method, "params": params}); err != nil {
		return errors.New("Unable to connect to the Codex usage reader")
	}
	for rpc.scanner.Scan() {
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(rpc.scanner.Bytes(), &reply) != nil || reply.ID != rpc.id {
			continue
		}
		if len(reply.Error) > 0 && string(reply.Error) != "null" {
			return errors.New("Codex returned no quota. Check sign-in or update the CLI.")
		}
		if result == nil {
			return nil
		}
		if json.Unmarshal(reply.Result, result) != nil {
			return errors.New("Unrecognized Codex usage data")
		}
		return nil
	}
	return errors.New("Codex usage connection closed")
}

func (rpc *codexRPC) read() (quota.Reading, error) {
	r := quota.Reading{Agent: "codex", Home: rpc.home}
	var identityReply struct {
		Account *struct{ Type, Email string } `json:"account"`
	}
	if err := rpc.call("account/read", map[string]bool{"refreshToken": false}, &identityReply); err != nil {
		return r, err
	}
	if identityReply.Account == nil {
		return r, errors.New("Sign in to Codex CLI first")
	}
	if identityReply.Account.Type != "chatgpt" {
		return r, errors.New("This sign-in method does not provide subscription quota")
	}
	var result struct {
		Limits    codexLimits                  `json:"rateLimits"`
		ByID      map[string]codexLimits       `json:"rateLimitsByLimitId"`
		Resets    *account.ResetCreditsSummary `json:"rateLimitResetCredits"`
		AccountID string                       `json:"accountId"`
	}
	if err := rpc.call("account/rateLimits/read", nil, &result); err != nil {
		return r, err
	}
	limits := result.Limits
	if general, ok := result.ByID["codex"]; ok {
		limits = general
	}
	r.Plan, r.Windows, r.Resets = limits.Plan, codexWindows(limits), result.Resets
	r.Account = identityReply.Account.Email
	if result.AccountID != "" {
		r.AccountKey = "id:" + result.AccountID
	} else if r.Account != "" {
		r.AccountKey = "email:" + strings.ToLower(r.Account)
	}
	if r.Resets != nil {
		r.Resets.Normalize(time.Now())
	}
	r.At = time.Now()
	if len(r.Windows) == 0 && r.Resets == nil {
		return r, errors.New("Subscription quota is unavailable for this account")
	}
	return r, nil
}

func codexWindows(limits codexLimits) []quota.Window {
	var windows []quota.Window
	for i, w := range []*codexWindow{limits.Primary, limits.Secondary} {
		if w == nil || w.Used == nil {
			continue
		}
		name := []string{"Short cycle", "Long cycle"}[i]
		if w.Mins > 0 {
			switch {
			case w.Mins%1440 == 0:
				name = fmt.Sprintf("%d days", w.Mins/1440)
			case w.Mins%60 == 0:
				name = fmt.Sprintf("%d hours", w.Mins/60)
			default:
				name = fmt.Sprintf("%d minutes", w.Mins)
			}
		}
		reset := ""
		if w.Reset > 0 {
			reset = time.Unix(w.Reset, 0).Local().Format("01-02 15:04")
		}
		windows = append(windows, quota.Window{Name: name, Used: quota.Percent(*w.Used), Reset: reset, DurationMins: w.Mins, ResetsAt: w.Reset})
	}
	return windows
}
