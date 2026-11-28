package pi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"miya/internal/processenv"
)

// Print asks pi once, as Miya's conversations run it (o's provider, model
// and extension), and returns its answer: with no tools, no project
// instructions, skills or MCP servers, and no session saved. o.Cwd is
// where it runs; system replaces pi's coding prompt.
func Print(ctx context.Context, o Options, system, prompt string) (string, error) {
	if o.Binary == "" {
		return "", errors.New("Could not find pi command")
	}
	bin, err := processenv.PinWSL(ctx, o.Binary)
	if err != nil {
		return "", err
	}
	o.SessionFile = ""
	args, env := launchIn(o, "--print", "--mode", "text", "--no-session", "--no-tools", "--no-context-files",
		"--no-skills", "--no-prompt-templates", "--no-mcp", "--no-themes", "--offline", "--system-prompt", system)
	cmd := processenv.CommandContext(ctx, bin, append(args, "--", prompt)...)
	cmd.Dir = o.Cwd
	cmd.Env = processenv.Environ(bin, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	processenv.Prepare(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pi: %v %s", err, lastLines(strings.TrimSpace(stderr.String()), 4))
	}
	return strings.TrimSpace(string(out)), nil
}
