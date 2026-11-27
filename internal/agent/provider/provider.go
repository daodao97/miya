// Package provider keeps the model providers configured in Miya — an API key,
// and a base URL for each protocol the provider serves — and which agent
// uses which. A provider reaches an agent only through the processes Miya
// starts: arguments and variables are appended after the user's launch
// command (launch.go). Nothing is written to an agent's own configuration
// or sign-in, so the CLIs run outside Miya, and an agent left on its own
// configuration, are unaffected.
package provider

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"miya/internal/agent/catalog"
	"miya/internal/appdir"
)

// API is a protocol a provider's base URL speaks.
type API string

const (
	// Anthropic is the Messages API; the base URL is what
	// ANTHROPIC_BASE_URL takes, without /v1.
	Anthropic API = "anthropic"
	// Responses is OpenAI's Responses API; the base URL ends in /v1.
	Responses API = "responses"
	// Chat is OpenAI's Chat Completions API; the base URL ends in /v1.
	Chat API = "chat"
)

// APIs lists the protocols in the order the editor offers them.
var APIs = []API{Anthropic, Responses, Chat}

// Agents lists the agents a provider can be turned on for.
var Agents = []string{catalog.Claude, catalog.Codex, catalog.Pi}

// agentAPIs are the protocols each agent speaks, the one it prefers first:
// Claude Code speaks Anthropic's API only, Codex the Responses API only
// (its chat wire API is gone), and pi all three.
var agentAPIs = map[string][]API{
	catalog.Claude: {Anthropic},
	catalog.Codex:  {Responses},
	catalog.Pi:     {Anthropic, Responses, Chat},
}

// Endpoint is one protocol a provider serves, at its base URL.
type Endpoint struct {
	API     API    `json:"api"`
	BaseURL string `json:"baseUrl"`
}

// EndpointFor is the endpoint among eps that agent uses: the first of the
// protocols it speaks, in its order of preference, that eps serve.
func EndpointFor(agent string, eps []Endpoint) (Endpoint, bool) {
	for _, api := range agentAPIs[agent] {
		for _, e := range eps {
			if e.API == api && e.BaseURL != "" {
				return e, true
			}
		}
	}
	return Endpoint{}, false
}

// AgentAPIs are the protocols agent speaks, the one it prefers first.
func AgentAPIs(agent string) []API { return agentAPIs[agent] }

// Supports reports whether agent can use a provider serving eps.
func Supports(agent string, eps []Endpoint) bool {
	_, ok := EndpointFor(agent, eps)
	return ok
}

// NeedsModels reports whether agent runs only the models a provider lists:
// pi registers just those, and Codex would otherwise ask for OpenAI's.
// Claude Code maps its own aliases onto the provider's first model, or
// sends Claude's names when it lists none.
func NeedsModels(agent string) bool { return agent == catalog.Codex || agent == catalog.Pi }

// Provider is one provider: its key, and an endpoint per protocol.
type Provider struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Endpoints []Endpoint `json:"endpoints,omitempty"`
	APIKey    string     `json:"apiKey,omitempty"`
	Models    []string   `json:"models,omitempty"`

	// LegacyAPI and LegacyBaseURL are a provider saved with one protocol,
	// read into Endpoints as it loads.
	LegacyAPI     API    `json:"api,omitempty"`
	LegacyBaseURL string `json:"baseUrl,omitempty"`
}

// Usable is whether the provider has what a request needs.
func (p Provider) Usable() bool { return p.APIKey != "" && len(p.Endpoints) > 0 }

// Model is the model the provider's agents start on: its first.
func (p Provider) Model() string {
	if len(p.Models) == 0 {
		return ""
	}
	return p.Models[0]
}

// Endpoint is the provider's base URL for api.
func (p Provider) Endpoint(api API) (string, bool) {
	for _, e := range p.Endpoints {
		if e.API == api && e.BaseURL != "" {
			return e.BaseURL, true
		}
	}
	return "", false
}

// Target is a provider as one agent uses it: through one endpoint.
type Target struct {
	Provider
	Endpoint `json:"-"`
}

// View is a provider as a window or phone sees it: never its key.
type View struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Endpoints []Endpoint `json:"endpoints,omitempty"`
	Models    []string   `json:"models,omitempty"`
	// KeyHint is the key's last four characters, "" without a key.
	KeyHint string `json:"keyHint,omitempty"`
}

func (p Provider) View() View {
	v := View{ID: p.ID, Name: p.Name, Endpoints: slices.Clone(p.Endpoints), Models: slices.Clone(p.Models)}
	if k := p.APIKey; k != "" {
		v.KeyHint = k[max(0, len(k)-4):]
	}
	return v
}

// Edit adds a provider (ID "") or changes one. An empty APIKey keeps the
// key it has, so a window that never holds the key can still save.
type Edit struct {
	ID        string     `json:"id,omitempty"`
	Name      string     `json:"name"`
	Endpoints []Endpoint `json:"endpoints"`
	APIKey    string     `json:"apiKey,omitempty"`
	Models    []string   `json:"models,omitempty"`
}

// Use chooses a provider for an agent; ID "" is the agent's own
// configuration.
type Use struct {
	Agent string `json:"agent"`
	ID    string `json:"id"`
}

// Config is everything kept: the providers, and the one each agent uses.
type Config struct {
	Providers []Provider        `json:"providers,omitempty"`
	Use       map[string]string `json:"use,omitempty"`
}

// Find returns the provider with id.
func (c Config) Find(id string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// For is the provider agent uses, through the endpoint it speaks: chosen,
// still there, serving a protocol the agent speaks and with a key.
// Otherwise the agent keeps its own configuration.
func (c Config) For(agent string) (Target, bool) {
	p, ok := c.Find(c.Use[agent])
	if !ok || !p.Usable() {
		return Target{}, false
	}
	e, ok := EndpointFor(agent, p.Endpoints)
	if !ok {
		return Target{}, false
	}
	return Target{Provider: p, Endpoint: e}, true
}

// Views are the providers without their keys.
func (c Config) Views() []View {
	out := make([]View, len(c.Providers))
	for i, p := range c.Providers {
		out[i] = p.View()
	}
	return out
}

// cleanEndpoints validates an edit's endpoints: one per protocol, each
// with an http(s) base URL, at least one in all.
func cleanEndpoints(eps []Endpoint) ([]Endpoint, error) {
	var out []Endpoint
	for _, e := range eps {
		e.BaseURL = strings.TrimRight(strings.TrimSpace(e.BaseURL), "/")
		if e.BaseURL == "" {
			continue
		}
		if !slices.Contains(APIs, e.API) {
			return nil, errors.New("unknown API " + string(e.API))
		}
		if !strings.HasPrefix(e.BaseURL, "http://") && !strings.HasPrefix(e.BaseURL, "https://") {
			return nil, errors.New("base URL must start with http:// or https://")
		}
		if slices.ContainsFunc(out, func(o Endpoint) bool { return o.API == e.API }) {
			return nil, errors.New("one base URL per API")
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, errors.New("turn on at least one API with its base URL")
	}
	// kept in the editor's order, whatever order they came in
	slices.SortFunc(out, func(a, b Endpoint) int { return slices.Index(APIs, a.API) - slices.Index(APIs, b.API) })
	return out, nil
}

// Apply saves e into the configuration and returns the provider's ID.
func (c *Config) Apply(e Edit) (string, error) {
	e.Name, e.APIKey = strings.TrimSpace(e.Name), strings.TrimSpace(e.APIKey)
	eps, err := cleanEndpoints(e.Endpoints)
	if err != nil {
		return "", err
	}
	models := make([]string, 0, len(e.Models))
	for _, m := range e.Models {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(models, m) {
			models = append(models, m)
		}
	}
	for i, p := range c.Providers {
		if e.ID == "" || p.ID != e.ID {
			continue
		}
		p.Name, p.Endpoints, p.Models = e.Name, eps, models
		if e.APIKey != "" {
			p.APIKey = e.APIKey
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		c.Providers[i] = p
		return p.ID, nil
	}
	if e.ID != "" {
		return "", errors.New("provider not found")
	}
	if e.APIKey == "" {
		return "", errors.New("API key is required")
	}
	p := Provider{ID: newID(), Name: e.Name, Endpoints: eps, APIKey: e.APIKey, Models: models}
	if p.Name == "" {
		p.Name = p.ID
	}
	c.Providers = append(c.Providers, p)
	return p.ID, nil
}

// Delete removes a provider, and any agent's choice of it.
func (c *Config) Delete(id string) {
	c.Providers = slices.DeleteFunc(c.Providers, func(p Provider) bool { return p.ID == id })
	for agent, chosen := range c.Use {
		if chosen == id {
			delete(c.Use, agent)
		}
	}
}

// Choose records the provider agent uses; "" returns it to its own
// configuration.
func (c *Config) Choose(u Use) error {
	if !slices.Contains(Agents, u.Agent) {
		return errors.New("unknown agent " + u.Agent)
	}
	if u.ID == "" {
		delete(c.Use, u.Agent)
		return nil
	}
	p, ok := c.Find(u.ID)
	if !ok {
		return errors.New("provider not found")
	}
	if !Supports(u.Agent, p.Endpoints) {
		return errors.New("this provider serves no API this agent speaks")
	}
	if c.Use == nil {
		c.Use = map[string]string{}
	}
	c.Use[u.Agent] = u.ID
	return nil
}

// newID is a provider's ID: short, and safe in a TOML key and in an
// environment variable's name once upper-cased.
func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "p" + hex.EncodeToString(b)
}

// Dir holds the configuration and the files made from it for launches.
func Dir() string { return filepath.Join(appdir.Dir(), "providers") }

func path() string { return filepath.Join(Dir(), "providers.json") }

// Load reads the configuration; a missing file is an empty one.
func Load() (Config, error) {
	var c Config
	data, err := os.ReadFile(path())
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	for i, p := range c.Providers {
		if len(p.Endpoints) == 0 && p.LegacyBaseURL != "" {
			api := p.LegacyAPI
			if api == "" {
				api = Anthropic
			}
			p.Endpoints = []Endpoint{{API: api, BaseURL: p.LegacyBaseURL}}
		}
		p.LegacyAPI, p.LegacyBaseURL = "", ""
		c.Providers[i] = p
	}
	return c, nil
}

// Save writes the configuration, readable by the user alone.
func Save(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path(), data)
}

// writePrivate replaces path with data, mode 0600, never leaving a partial
// file for a launch that reads it at the same moment.
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if current, err := os.ReadFile(path); err == nil && string(current) == string(data) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	_, writeErr := f.Write(data)
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return writeErr
	}
	return os.Rename(f.Name(), path)
}
