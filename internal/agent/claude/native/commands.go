package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"

	"miya/internal/processenv"
)

// SlashCommand is a slash command Claude Code offers: built in, the user's or the
// project's own (.claude/commands), or a skill.
type SlashCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint"`
}

// Model is a model Claude Code offers in its model picker, as it reports
// them on initialize: aliases, full IDs and the user's configured models.
type Model struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

// Catalog is what Claude Code reports on initialize that Miya offers.
type Catalog struct {
	Commands []SlashCommand
	Models   []Model
}

// commandsSettle is how long to wait after initialize for the list that
// plugins and MCP servers complete (system commands_changed).
const commandsSettle = 2500 * time.Millisecond

// ListCommands asks Claude Code in cwd for its slash commands.
func ListCommands(ctx context.Context, binary string, args, env []string, cwd string) ([]SlashCommand, error) {
	cat, err := ReadCatalog(ctx, binary, args, env, cwd)
	return cat.Commands, err
}

// ReadCatalog asks Claude Code in cwd for its slash commands and models: it
// starts the CLI, initializes it and stops it, sending no message.
func ReadCatalog(ctx context.Context, binary string, args, env []string, cwd string) (Catalog, error) {
	if binary == "" {
		binary = "claude"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	a := append(append([]string(nil), args...), "--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--permission-mode", "default")
	cmd := exec.CommandContext(ctx, binary, a...)
	cmd.Dir = cwd
	cmd.Env = processenv.Environ(binary, env...)
	configureProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return Catalog{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Catalog{}, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return Catalog{}, err
	}
	defer func() {
		in.Close()
		cancel()
		cmd.Wait()
	}()
	if _, err := io.WriteString(in, `{"type":"control_request","request_id":"commands","request":{"subtype":"initialize","hooks":{}}}`+"\n"); err != nil {
		return Catalog{}, err
	}
	type frame struct {
		Type     string         `json:"type"`
		Subtype  string         `json:"subtype"`
		Commands []SlashCommand `json:"commands"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Error     string `json:"error"`
			Response  struct {
				Commands []SlashCommand `json:"commands"`
				Models   []Model        `json:"models"`
			} `json:"response"`
		} `json:"response"`
	}
	frames := make(chan frame)
	go func() {
		defer close(frames)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 65536), 16<<20)
		for sc.Scan() {
			var f frame
			if json.Unmarshal(sc.Bytes(), &f) != nil {
				continue
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	var cmds, changed []SlashCommand
	var models []Model
	got := false
	var settle <-chan time.Time
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				if got {
					return Catalog{cmds, models}, nil
				}
				return Catalog{}, errors.New("Claude Code returned no command list")
			}
			switch {
			case f.Type == "control_response" && f.Response.RequestID == "commands":
				if f.Response.Subtype == "error" {
					return Catalog{}, errors.New(f.Response.Error)
				}
				cmds, models, got = f.Response.Response.Commands, f.Response.Response.Models, true
				if changed != nil {
					return Catalog{changed, models}, nil
				}
				settle = time.After(commandsSettle)
			case f.Type == "system" && f.Subtype == "commands_changed" && len(f.Commands) > 0:
				if got {
					return Catalog{f.Commands, models}, nil
				}
				changed = f.Commands
			}
		case <-settle:
			return Catalog{cmds, models}, nil
		case <-ctx.Done():
			if got {
				return Catalog{cmds, models}, nil
			}
			return Catalog{}, ctx.Err()
		}
	}
}
