package agent

import (
	"strings"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/composer"
	"miya/internal/model"
)

// CommandCatalog can be read without I/O (load=false), e.g. while styling a draft.
// Known distinguishes an empty catalog from metadata that is still loading.
type CommandCatalog struct {
	Choices        []model.ComposerChoice
	PhoneChoices   []model.ComposerChoice
	Known          bool
	Native         bool
	LoadingMessage string
}

var hostCommands = map[string]bool{"new": true, "model": true, "permissions": true, "mention": true, "diff": true, "status": true}

func (c CommandCatalog) TakesCommand(command string) bool {
	name, _, _ := strings.Cut(strings.TrimSpace(command), " ")
	return c.Native && !IsHostCommand(name)
}
func (c CommandCatalog) Filter(query string) []model.ComposerChoice {
	query = strings.ToLower(query)
	var out []model.ComposerChoice
	for _, choice := range c.Choices {
		if strings.Contains(strings.ToLower(choice.Name), query) {
			out = append(out, choice)
		}
	}
	return out
}

// MergeCommands gives host controls priority, preserving native descriptions and
// spelling for every remaining command. It never mutates cached slices.
func MergeCommands(host, native []model.ComposerChoice) []model.ComposerChoice {
	seen := map[string]bool{}
	var out []model.ComposerChoice
	for _, list := range [][]model.ComposerChoice{host, native} {
		for _, choice := range list {
			key := strings.ToLower(choice.Name)
			if choice.Name != "" && !seen[key] {
				seen[key] = true
				out = append(out, choice)
			}
		}
	}
	return out
}

type ModelOption struct {
	Value         string   `json:"value"`
	Label         string   `json:"label"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort"`
}

// DefaultCommands is also the compatibility projection for servers predating
// explicit command metadata. Merely reading it performs no discovery.
func DefaultCommands(id string) CommandCatalog {
	d := agentmeta.For(id)
	catalog := CommandCatalog{Known: !d.NativeCommands, Native: d.NativeCommands}
	if !d.NativeCommands {
		catalog.Choices = composer.ComposerCommands
		catalog.PhoneChoices = []model.ComposerChoice{composer.ComposerCommands[0]}
	}
	return catalog
}
func CommandKey(id, project string) string { return id + "\x00" + project }

func IsHostCommand(name string) bool { return hostCommands[name] }
