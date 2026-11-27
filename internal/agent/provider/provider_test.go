package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"miya/internal/agent/catalog"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
}

func TestApplyChooseAndPersist(t *testing.T) {
	isolate(t)
	var c Config
	if _, err := c.Apply(Edit{Name: "x", Endpoints: []Endpoint{{API: Anthropic, BaseURL: "https://a.example"}}}); err == nil {
		t.Fatal("a new provider without a key was accepted")
	}
	if _, err := c.Apply(Edit{Name: "x", APIKey: "k", Endpoints: []Endpoint{{API: Anthropic, BaseURL: " "}}}); err == nil {
		t.Fatal("a provider without a base URL was accepted")
	}
	if _, err := c.Apply(Edit{Name: "x", APIKey: "k", Endpoints: []Endpoint{{API: Chat, BaseURL: "https://a/v1"}, {API: Chat, BaseURL: "https://b/v1"}}}); err == nil {
		t.Fatal("two base URLs for one API were accepted")
	}
	// one provider serving both protocols, given out of order
	id, err := c.Apply(Edit{Name: " Kimi ", APIKey: "sk-secret-1234", Models: []string{" k2 ", "k2", ""}, Endpoints: []Endpoint{
		{API: Responses, BaseURL: "https://a.example/v1/"},
		{API: Anthropic, BaseURL: "https://a.example/anthropic/"},
		{API: Chat, BaseURL: ""},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Find(id)
	wantEps := []Endpoint{{API: Anthropic, BaseURL: "https://a.example/anthropic"}, {API: Responses, BaseURL: "https://a.example/v1"}}
	if p.Name != "Kimi" || !slices.Equal(p.Endpoints, wantEps) || !slices.Equal(p.Models, []string{"k2"}) {
		t.Fatalf("provider not normalized: %+v", p)
	}
	for _, agent := range Agents {
		if err := c.Choose(Use{Agent: agent, ID: id}); err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
	}
	for agent, want := range map[string]Endpoint{catalog.Claude: wantEps[0], catalog.Codex: wantEps[1], catalog.Pi: wantEps[0]} {
		if got, ok := c.For(agent); !ok || got.Endpoint != want || got.APIKey != "sk-secret-1234" {
			t.Errorf("%s uses %+v, %v; want %+v", agent, got.Endpoint, ok, want)
		}
	}
	// a window saves without the key it never holds; dropping Anthropic
	// leaves Claude Code on its own and moves pi to Responses
	if _, err := c.Apply(Edit{ID: id, Name: "Kimi", Endpoints: wantEps[1:]}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.For(catalog.Claude); ok {
		t.Fatal("Claude Code kept a provider without an Anthropic endpoint")
	}
	if got, ok := c.For(catalog.Pi); !ok || got.API != Responses || got.APIKey != "sk-secret-1234" {
		t.Fatalf("pi = %+v, %v", got, ok)
	}
	if err := c.Choose(Use{Agent: catalog.Claude, ID: id}); err == nil {
		t.Fatal("Claude Code was given a provider without an Anthropic endpoint")
	}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, %v", info.Mode().Perm(), err)
	}
	loaded, err := Load()
	if err != nil || len(loaded.Providers) != 1 || loaded.Use[catalog.Codex] != id {
		t.Fatalf("loaded %+v, %v", loaded, err)
	}
	if v := loaded.Views()[0]; v.KeyHint != "1234" || len(v.Endpoints) != 1 {
		t.Fatalf("view = %+v", v)
	}
	view, _ := json.Marshal(loaded.Views())
	if strings.Contains(string(view), "sk-secret") {
		t.Fatalf("view carries the key: %s", view)
	}
	loaded.Delete(id)
	if len(loaded.Providers) != 0 || len(loaded.Use) != 0 {
		t.Fatalf("delete left %+v", loaded)
	}
}

func TestLoadReadsASingleProtocolProvider(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"providers":[{"id":"p1","name":"Old","api":"responses","baseUrl":"https://o/v1","apiKey":"k"}],"use":{"codex":"p1"}}`
	if err := os.WriteFile(path(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := c.For(catalog.Codex); !ok || got.BaseURL != "https://o/v1" || got.API != Responses {
		t.Fatalf("codex = %+v, %v", got, ok)
	}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path())
	if strings.Contains(string(data), `"api": "responses",\n      "baseUrl"`) || !strings.Contains(string(data), `"endpoints"`) {
		t.Fatalf("saved = %s", data)
	}
}

func TestClaudeArgsMergesUserSettings(t *testing.T) {
	isolate(t)
	user := filepath.Join(t.TempDir(), "user.json")
	if err := os.WriteFile(user, []byte(`{"hooks":{"Stop":[]},"env":{"FOO":"1","ANTHROPIC_BASE_URL":"https://old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := Target{Provider: Provider{ID: "p1", APIKey: "sk-x", Models: []string{"glm-4.6"}}, Endpoint: Endpoint{API: Anthropic, BaseURL: "https://new"}}
	args, err := ClaudeArgs(target, []string{"--verbose", "--settings", user, "--settings=/missing.json"})
	if err != nil {
		t.Fatal(err)
	}
	n := len(args)
	if n != 4 || args[0] != "--verbose" || args[1] != "--settings=/missing.json" || args[n-2] != "--settings" {
		t.Fatalf("args = %q", args)
	}
	data, err := os.ReadFile(args[n-1])
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Hooks map[string]any    `json:"hooks"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Hooks == nil || got.Env["FOO"] != "1" {
		t.Fatalf("user settings lost: %s", data)
	}
	for k, want := range map[string]string{"ANTHROPIC_BASE_URL": "https://new", "ANTHROPIC_AUTH_TOKEN": "sk-x", "ANTHROPIC_API_KEY": "", "CLAUDE_CODE_USE_BEDROCK": "", "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-4.6"} {
		if v, ok := got.Env[k]; !ok || v != want {
			t.Errorf("env %s = %q, want %q", k, v, want)
		}
	}
}

func TestCodexLaunch(t *testing.T) {
	c := Config{Providers: []Provider{
		{ID: "pa", Name: "A \"quoted\"", APIKey: "ka", Models: []string{"m-a"}, Endpoints: []Endpoint{{API: Anthropic, BaseURL: "https://a/anthropic"}, {API: Responses, BaseURL: "https://a/v1"}}},
		{ID: "pb", Name: "B", APIKey: "kb", Endpoints: []Endpoint{{API: Responses, BaseURL: "https://b/v1"}}},
		{ID: "pc", Name: "C", APIKey: "kc", Endpoints: []Endpoint{{API: Anthropic, BaseURL: "https://c"}}},
	}, Use: map[string]string{catalog.Codex: "pa"}}
	l := CodexLaunch(c)
	args := strings.Join(l.Args, " ")
	for _, want := range []string{
		`model_providers.miya-pa.name="A \"quoted\""`,
		`model_providers.miya-pa.base_url="https://a/v1"`,
		`model_providers.miya-pa.wire_api="responses"`,
		`model_providers.miya-pa.env_key="MIYA_PROVIDER_KEY_PA"`,
		`model_providers.miya-pb.base_url="https://b/v1"`,
		`model_provider="miya-pa"`,
		`model="m-a"`,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %s: %s", want, args)
		}
	}
	if strings.Contains(args, "miya-pc") || strings.Contains(args, "anthropic") || strings.Contains(args, "ka\"") {
		t.Errorf("args carry an Anthropic endpoint or a key: %s", args)
	}
	if !slices.Equal(l.Env, []string{"MIYA_PROVIDER_KEY_PA=ka", "MIYA_PROVIDER_KEY_PB=kb"}) {
		t.Errorf("env = %q", l.Env)
	}
	// Codex on its own configuration still gets the tables for old threads
	delete(c.Use, catalog.Codex)
	if args := strings.Join(CodexLaunch(c).Args, " "); strings.Contains(args, "model_provider=") || !strings.Contains(args, "miya-pa.base_url") {
		t.Errorf("unchosen args = %s", args)
	}
}

func TestFetchModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, e := range []Endpoint{{API: Anthropic, BaseURL: server.URL}, {API: Responses, BaseURL: server.URL + "/v1"}} {
		got, err := FetchModels(context.Background(), Provider{APIKey: "k", Endpoints: []Endpoint{e}})
		if err != nil || !slices.Equal(got, []string{"m1", "m2"}) {
			t.Errorf("%s: %q, %v", e.API, got, err)
		}
	}
	// an endpoint that fails gives way to the next
	got, err := FetchModels(context.Background(), Provider{APIKey: "k", Endpoints: []Endpoint{{API: Chat, BaseURL: server.URL + "/wrong"}, {API: Responses, BaseURL: server.URL + "/v1"}}})
	if err != nil || len(got) != 2 {
		t.Errorf("fallback: %q, %v", got, err)
	}
	if _, err := FetchModels(context.Background(), Provider{APIKey: "bad", Endpoints: []Endpoint{{API: Chat, BaseURL: server.URL + "/v1"}}}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("bad key: %v", err)
	}
}
