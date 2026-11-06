package testcli

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func Main(m *testing.M) {
	if mode := os.Getenv("USAGE_TEST_HELPER"); mode != "" {
		if mode == "tree-child" {
			_ = os.WriteFile(os.Getenv("USAGE_TREE_READY"), []byte("ready"), 0600)
			time.Sleep(600 * time.Millisecond)
			_ = os.WriteFile(os.Getenv("USAGE_TREE_SURVIVED"), []byte("survived"), 0600)
			os.Exit(0)
		}
		if mode == "tree" {
			child := exec.Command(os.Args[0])
			child.Env = append(os.Environ(), "USAGE_TEST_HELPER=tree-child")
			_ = child.Run()
			os.Exit(0)
		}
		if mode == "timeout" {
			time.Sleep(time.Minute)
			os.Exit(1)
		}
		if mode == "claude" {
			if !reflect.DeepEqual(os.Args[1:], []string{"-p", "/usage", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--setting-sources", "", "--no-session-persistence"}) {
				os.Exit(2)
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "result", "result": "Current session: 13% used · resets Oct 28 at 3pm (Asia/Shanghai)\nCurrent week (all models): 42% used\nCurrent week (Sonnet): 78% used"})
			os.Exit(0)
		}
		if strings.HasPrefix(mode, "reset-") {
			runResetHelper(mode)
			os.Exit(0)
		}
		if !reflect.DeepEqual(os.Args[1:], []string{"app-server", "--listen", "stdio://"}) {
			os.Exit(3)
		}
		dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
		for _, method := range []string{"initialize", "initialized", "account/read", "account/rateLimits/read"} {
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if dec.Decode(&req) != nil || req.Method != method {
				os.Exit(4)
			}
			if method == "initialized" {
				continue
			}
			var result any = map[string]any{}
			switch method {
			case "account/read":
				typ := "chatgpt"
				if mode == "api" {
					typ = "apiKey"
				}
				result = map[string]any{"account": map[string]string{"type": typ}}
			case "account/rateLimits/read":
				// An unrelated notification must never be mistaken for the reply.
				_ = enc.Encode(map[string]string{"method": "account/updated"})
				_ = json.Unmarshal([]byte(`{"rateLimits":{"planType":"wrong","primary":{"usedPercent":99}},"rateLimitsByLimitId":{"codex":{"planType":"pro","primary":{"usedPercent":17,"windowDurationMins":300,"resetsAt":1900000000},"secondary":{"usedPercent":42,"windowDurationMins":10080}}}}`), &result)
				if mode == "codex-resets" {
					result.(map[string]any)["rateLimitResetCredits"] = map[string]any{"availableCount": 3, "credits": []any{map[string]any{"id": "later", "resetType": "codexRateLimits", "status": "available", "expiresAt": 2000000000}, map[string]any{"id": "first", "resetType": "codexRateLimits", "status": "available", "expiresAt": 1900000000}}}
				}
			}
			_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
		}
		// Exit only when the reader closes its owned server's input.
		var ignored any
		_ = dec.Decode(&ignored)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runResetHelper(mode string) {
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	consumed := false
	for {
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		}
		// initialize and account/read carry non-string fields, so decode the
		// request envelope separately from the consume parameters.
		var raw struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if dec.Decode(&raw) != nil {
			return
		}
		req.ID, req.Method = raw.ID, raw.Method
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
		case "initialized":
			continue
		case "account/read":
			result = map[string]any{"account": map[string]string{"type": "chatgpt", "email": "fixture@example.invalid"}}
		case "account/rateLimits/read":
			id := "fixture"
			if mode == "reset-switched" {
				id = "other"
			}
			used, count := 80, 2
			if consumed {
				used, count = 0, 1
			}
			result = map[string]any{"accountId": id, "rateLimits": map[string]any{"planType": "pro", "primary": map[string]any{"usedPercent": used, "windowDurationMins": 300}}, "rateLimitResetCredits": map[string]any{"availableCount": count}}
			if mode == "reset-refresh-failed" && consumed {
				_ = enc.Encode(map[string]any{"id": req.ID, "error": map[string]string{"message": "fixture read failure"}})
				continue
			}
		case "account/rateLimitResetCredit/consume":
			if consumed || mode == "reset-switched" {
				os.Exit(8)
			}
			if json.Unmarshal(raw.Params, &req.Params) != nil || req.Params["idempotencyKey"] == "" {
				os.Exit(9)
			}
			f, err := os.OpenFile(os.Getenv("USAGE_RESET_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(10)
			}
			_ = json.NewEncoder(f).Encode(req.Params)
			f.Close()
			consumed = true
			outcome := os.Getenv("USAGE_RESET_OUTCOME")
			if outcome == "" {
				outcome = "reset"
			}
			if mode == "reset-ambiguous" {
				_ = enc.Encode(map[string]any{"id": req.ID, "error": map[string]string{"message": "fixture ambiguous result"}})
				continue
			}
			result = map[string]string{"outcome": outcome}
		default:
			os.Exit(11)
		}
		if enc.Encode(map[string]any{"id": req.ID, "result": result}) != nil {
			return
		}
	}
}

func Helper(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv("USAGE_TEST_HELPER", mode)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return bin
}
