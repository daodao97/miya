package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Provider is the Anthropic-compatible endpoint Miya's Claude Code command
// uses. Pi reuses it, so one proxy or account serves both agents.
type Provider struct {
	BaseURL string
	// Token is ANTHROPIC_AUTH_TOKEN (sent as a bearer token) or, when that is
	// unset, ANTHROPIC_API_KEY.
	Token  string
	Bearer bool
	// Model is ANTHROPIC_MODEL, when the Claude settings pin one.
	Model string
	// Source says where the endpoint came from, for the settings page.
	Source string
}

// Usable is whether pi can be pointed at the provider.
func (p Provider) Usable() bool { return p.BaseURL != "" && p.Token != "" }

// ProviderFromClaude resolves the provider of a Claude Code command the way
// Claude Code itself layers its configuration: the process environment, the
// user settings' env, the env of each --settings value, then NAME=value words
// of the command line.
func ProviderFromClaude(args, env []string) Provider {
	values := map[string]string{}
	sources := map[string]string{}
	set := func(k, v, source string) {
		values[k], sources[k] = v, source
	}
	for _, k := range providerKeys {
		if v := os.Getenv(k); v != "" {
			set(k, v, "environment")
		}
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			configDir = v
		}
	}
	if configDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			configDir = filepath.Join(home, ".claude")
		}
	}
	if configDir != "" {
		path := filepath.Join(configDir, "settings.json")
		for k, v := range settingsEnv(path) {
			set(k, v, path)
		}
	}
	for i := 0; i < len(args); i++ {
		value, ok := "", false
		if args[i] == "--settings" && i+1 < len(args) {
			i++
			value, ok = args[i], true
		} else if v, cut := strings.CutPrefix(args[i], "--settings="); cut {
			value, ok = v, true
		}
		if !ok {
			continue
		}
		source := value
		if strings.HasPrefix(strings.TrimSpace(value), "{") {
			source = "--settings"
		}
		for k, v := range settingsEnv(value) {
			set(k, v, source)
		}
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if isProviderKey(k) {
			set(k, v, "launch command")
		}
	}
	p := Provider{BaseURL: strings.TrimRight(strings.TrimSpace(values["ANTHROPIC_BASE_URL"]), "/"), Model: strings.TrimSpace(values["ANTHROPIC_MODEL"]), Source: sources["ANTHROPIC_BASE_URL"]}
	if t := strings.TrimSpace(values["ANTHROPIC_AUTH_TOKEN"]); t != "" {
		p.Token, p.Bearer = t, true
	} else {
		p.Token = strings.TrimSpace(values["ANTHROPIC_API_KEY"])
	}
	return p
}

var providerKeys = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"}

func isProviderKey(k string) bool { return slices.Contains(providerKeys, k) }

// settingsEnv reads the provider variables of a Claude settings file, or of
// inline settings JSON as --settings also accepts.
func settingsEnv(value string) map[string]string {
	data := []byte(value)
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		var err error
		if data, err = os.ReadFile(value); err != nil {
			return nil
		}
	}
	var settings struct {
		Env map[string]any `json:"env"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range settings.Env {
		if s, ok := v.(string); ok && isProviderKey(k) {
			out[k] = s
		}
	}
	return out
}
