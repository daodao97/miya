package provider

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/agent/catalog"
)

// Launch is what a process gets appended, after the user's launch command,
// to reach a provider.
type Launch struct {
	Args []string
	Env  []string
}

// claudeCloudSwitches send Claude Code's requests to a cloud's endpoint in
// place of ANTHROPIC_BASE_URL (magpie's agentenv.ClaudeProviders, Claude
// Code 2.1.300). A provider turns them off for the processes it reaches.
var claudeCloudSwitches = []string{
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD", "CLAUDE_CODE_USE_MANTLE",
}

// claudeEnv is the env block that points Claude Code at t.
func claudeEnv(t Target) map[string]any {
	env := map[string]any{
		"ANTHROPIC_BASE_URL":   t.BaseURL,
		"ANTHROPIC_AUTH_TOKEN": t.APIKey,
		// a key of the user's own would otherwise go along as x-api-key
		"ANTHROPIC_API_KEY": "",
	}
	for _, k := range claudeCloudSwitches {
		env[k] = ""
	}
	// The model pickers name Claude's models and aliases, which a provider
	// may not serve: each alias, and the default, resolve to its model.
	if m := t.Model(); m != "" {
		for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
			env[k] = m
		}
	}
	return env
}

// ClaudeArgs gives a Claude Code command line t, the provider's Anthropic
// endpoint. The variables go in a settings file passed with --settings,
// which outranks the user's settings.json, where an env block of their own
// would otherwise win over the process' variables. The --settings values
// the command already has are merged into that file beneath the provider,
// since Claude Code reads one; one Miya cannot read is left on the command
// line before Miya's.
func ClaudeArgs(t Target, args []string) ([]string, error) {
	merged := map[string]any{}
	env := map[string]any{}
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		value, ok := "", false
		if args[i] == "--settings" && i+1 < len(args) {
			value, ok = args[i+1], true
		} else if v, cut := strings.CutPrefix(args[i], "--settings="); cut {
			value, ok = v, true
		}
		if !ok {
			out = append(out, args[i])
			continue
		}
		settings, read := readSettings(value)
		if !read {
			out = append(out, args[i])
			if args[i] == "--settings" {
				out = append(out, args[i+1])
				i++
			}
			continue
		}
		if args[i] == "--settings" {
			i++
		}
		if e, isMap := settings["env"].(map[string]any); isMap {
			maps.Copy(env, e)
		}
		maps.Copy(merged, settings)
	}
	maps.Copy(env, claudeEnv(t))
	merged["env"] = env
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(Dir(), "claude-"+t.ID+".json")
	if err := writePrivate(path, data); err != nil {
		return nil, err
	}
	return append(out, "--settings", path), nil
}

// readSettings reads a --settings value: a file, or inline JSON.
func readSettings(value string) (map[string]any, bool) {
	data := []byte(value)
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		if strings.HasPrefix(value, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				value = filepath.Join(home, value[2:])
			}
		}
		var err error
		if data, err = os.ReadFile(value); err != nil {
			return nil, false
		}
	}
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil || settings == nil {
		return nil, false
	}
	return settings, true
}

// CodexProviderID is the name a provider has among Codex's model_providers.
func CodexProviderID(p Provider) string { return "miya-" + p.ID }

// codexKeyVar is the variable a Codex provider table reads its key from.
func codexKeyVar(p Provider) string {
	return "MIYA_PROVIDER_KEY_" + strings.ToUpper(strings.ReplaceAll(p.ID, "-", "_"))
}

// CodexLaunch gives a Codex app-server every provider serving the
// Responses API as a table of model_providers, as config overrides, and
// the chosen one as its model_provider. Codex keeps the provider a thread
// was started on and cannot open it again without that table, so each
// table is passed whether it is chosen or not: a conversation started on a
// provider still opens after Codex is moved to another or back to its own
// configuration. Keys travel in the environment, never on the command line.
func CodexLaunch(c Config) Launch {
	var l Launch
	for _, p := range c.Providers {
		base, ok := p.Endpoint(Responses)
		if !ok || p.APIKey == "" {
			continue
		}
		table := "model_providers." + CodexProviderID(p)
		l.Args = append(l.Args,
			"-c", table+".name="+tomlString(p.Name),
			"-c", table+".base_url="+tomlString(base),
			"-c", table+".wire_api="+tomlString("responses"),
			"-c", table+".env_key="+tomlString(codexKeyVar(p)),
		)
		l.Env = append(l.Env, codexKeyVar(p)+"="+p.APIKey)
	}
	if t, ok := c.For(catalog.Codex); ok {
		l.Args = append(l.Args, "-c", "model_provider="+tomlString(CodexProviderID(t.Provider)))
		if m := t.Model(); m != "" {
			l.Args = append(l.Args, "-c", "model="+tomlString(m))
		}
	}
	return l
}

// tomlString quotes s as a TOML basic string, whose escapes are JSON's.
func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// PiAPI is pi's name for an API, as its providers' api field takes it.
func PiAPI(api API) string {
	switch api {
	case Responses:
		return "openai-responses"
	case Chat:
		return "openai-completions"
	}
	return "anthropic-messages"
}
