// Package agentmeta contains the built-in agent manifest shared by adapters,
// conversation references and clients. It performs no discovery or native I/O.
package agentmeta

import (
	_ "embed"
	"strings"
	"time"
)

const (
	Codex  = "codex"
	Claude = "claude-code"
)

type Icon struct {
	SVG, Color string
	PNG        []byte `json:"-"`
}

type Capabilities struct {
	Models, Effort, Quota, ThreadCommands, StallHint bool
}

// Descriptor describes presentation and routing without exposing a native client.
// NativeCommands means slash commands other than host controls go to the agent.
type Descriptor struct {
	ID, Name       string
	Icon           Icon
	Capabilities   Capabilities
	NativeCommands bool
	StallAfter     time.Duration
	UsageID        string
	UsageName      string
	UsageReset     bool
	Aliases        []string
}

//go:embed assets/codex.png
var codexPNG []byte

var builtins = []Descriptor{
	{ID: Codex, Name: "Codex", Icon: Icon{PNG: codexPNG}, Capabilities: Capabilities{Models: true, Effort: true, Quota: true, ThreadCommands: true}, UsageID: "codex", UsageName: "Codex", UsageReset: true},
	{ID: Claude, Name: "Claude Code", Icon: Icon{SVG: "claude-code", Color: "#c46b4d"}, Capabilities: Capabilities{Models: true, Effort: true, StallHint: true}, NativeCommands: true, StallAfter: 60 * time.Second, UsageID: "claude", UsageName: "Claude", Aliases: []string{"claude", "claude code"}},
}

func All() []Descriptor   { return append([]Descriptor(nil), builtins...) }
func Default() Descriptor { return builtins[0] }
func Lookup(id string) (Descriptor, bool) {
	for _, d := range builtins {
		if d.ID == id {
			return d, true
		}
	}
	return Descriptor{}, false
}
func For(id string) Descriptor {
	if d, ok := Lookup(id); ok {
		return d
	}
	return Default()
}
func IDs() []string {
	var out []string
	for _, d := range builtins {
		out = append(out, d.ID)
	}
	return out
}
func Normalize(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, d := range builtins {
		if id == d.ID {
			return d.ID
		}
		for _, alias := range d.Aliases {
			if id == alias {
				return d.ID
			}
		}
	}
	return ""
}
func Usage() []Descriptor {
	var out []Descriptor
	for _, d := range builtins {
		if d.UsageID != "" {
			out = append(out, d)
		}
	}
	return out
}
func UsageFor(id string) Descriptor {
	for _, d := range builtins {
		if d.UsageID == id {
			return d
		}
	}
	return Descriptor{ID: id, Name: id, UsageID: id, UsageName: id}
}

// LegacyCommandAgent is the untagged native command catalog in older phone protocols.
func LegacyCommandAgent() string { return Claude }
