package automation

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"miya/internal/textutil"
)

// Ask is an approval request from the conversation of an automation run,
// as the agent sent it (docs/action-review.md).
type Ask struct {
	// Tool is Claude Code's tool name, Input its input; Tool is empty for a
	// Codex request.
	Tool  string
	Input json.RawMessage
	// Command is what the request asks for, for the run's record.
	Command string
}

// Answer is what Miya answers an Ask with when no person is asked.
type Answer struct {
	// Decided is false when the request waits for a person as before.
	Decided bool
	Allow   bool
	// Message tells the agent why it was refused and what to do instead.
	Message string
}

// maxRefused is how many refused actions a run keeps.
const maxRefused = 20

// Unattended answers an approval request from the conversation thread when
// it belongs to a running automation with an access level. A refusal is
// recorded on the run.
func (rn *Runner) Unattended(thread int, ask Ask) Answer {
	if thread == 0 {
		return Answer{}
	}
	for id, r := range rn.Active {
		if r.Thread != thread {
			continue
		}
		i, ok := rn.Store.Find(id)
		if !ok || rn.Store.Automations[i].Access == "" {
			return Answer{}
		}
		a := rn.Store.Automations[i]
		dir := textutil.FirstNonEmpty(r.dir, a.Project)
		answer, what := decide(a.Access, dir, ask)
		if !answer.Decided && a.Access == AccessExternal && what != "" {
			return rn.review(a, r, dir, what)
		}
		if answer.Decided && !answer.Allow {
			rn.updateRun(id, r, func(run *Run) {
				if len(run.Refused) < maxRefused {
					run.Refused = append(run.Refused, textutil.ClipRunes(what, 200))
				}
			})
		}
		return answer
	}
	return Answer{}
}

// decide answers ask for an automation with access, whose run works in
// dir, and says what was asked for.
func decide(access Access, dir string, ask Ask) (Answer, string) {
	local, what := classify(dir, ask)
	if ask.Tool == "ExitPlanMode" && access == AccessRead {
		return Answer{Decided: true, Message: "This automation is read only (access: read), so there is no plan to carry out. Report what you found with report_result, and set needs_user if a change should be made."}, "leave plan mode"
	}
	if strings.HasPrefix(ask.Tool, "mcp__miya__") {
		return Answer{Decided: true, Allow: true}, what
	}
	switch {
	case local && access != AccessRead:
		return Answer{Decided: true, Allow: true}, what
	case local:
		return Answer{Decided: true, Message: fmt.Sprintf("Miya refused %s: this automation is read only (access: read). Do not try another way to change anything. Report what you found with report_result, and set needs_user if a change should be made.", what)}, what
	case access == AccessExternal:
		// Unattended sends it for review.
		return Answer{}, what
	}
	return Answer{Decided: true, Message: fmt.Sprintf("Miya refused %s: this automation may not act outside the project (access: %s). Do not try another way to do it. Finish the work that does not depend on it, and set needs_user in report_result with what should be done.", what, access)}, what
}

// classify says whether ask stays in the run's directory, and names it.
func classify(dir string, ask Ask) (bool, string) {
	if ask.Tool == "" {
		// Codex asks only for what its workspace sandbox does not allow.
		return false, textOr(strings.TrimSpace(ask.Command), "an action outside the sandbox")
	}
	var in struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		URL          string `json:"url"`
	}
	_ = json.Unmarshal(ask.Input, &in)
	switch ask.Tool {
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		path := textutil.FirstNonEmpty(in.FilePath, in.NotebookPath)
		return path != "" && within(dir, path), ask.Tool + " " + path
	case "Bash":
		return in.Command != "" && !OutsideCommand(in.Command), in.Command
	}
	return false, strings.TrimSpace(ask.Tool + " " + in.URL)
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	if dir == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// OutsideCommand reports whether a shell command may act outside the
// computer: push, publish, open or comment on pull requests and issues,
// send messages or write over the network. It errs toward outside, and is
// no security boundary: a command can hide what it does.
func OutsideCommand(command string) bool {
	for _, segment := range splitShell(command) {
		if outsideWords(strings.Fields(segment)) {
			return true
		}
	}
	return false
}

// splitShell splits a command line at the operators that start another
// command, including command substitution.
func splitShell(command string) []string {
	replacer := strings.NewReplacer("\n", ";", "&&", ";", "||", ";", "|", ";", "&", ";", "$(", ";", "`", ";", "(", ";", ")", ";", "{", ";", "}", ";")
	return strings.Split(replacer.Replace(command), ";")
}

// wrappers run the command that follows them.
var wrappers = map[string]bool{"sudo": true, "env": true, "command": true, "exec": true, "time": true, "nohup": true, "xargs": true, "nice": true, "timeout": true, "then": true, "do": true, "else": true, "!": true}

// alwaysOutside are programs whose use reaches outside whatever the
// arguments.
var alwaysOutside = map[string]bool{"ssh": true, "scp": true, "sftp": true, "ftp": true, "telnet": true, "nc": true, "ncat": true, "kubectl": true, "lark-cli": true, "mail": true, "mailx": true, "sendmail": true, "mutt": true, "osascript": true, "twine": true}

// outsideSubcommands are programs whose listed subcommands reach outside.
var outsideSubcommands = map[string][]string{
	"git":       {"push", "send-email", "request-pull"},
	"npm":       {"publish", "unpublish", "deprecate", "owner", "dist-tag"},
	"pnpm":      {"publish"},
	"yarn":      {"publish", "npm"},
	"bun":       {"publish"},
	"cargo":     {"publish", "yank", "owner"},
	"gem":       {"push", "yank"},
	"docker":    {"push", "login"},
	"podman":    {"push", "login"},
	"helm":      {"install", "upgrade", "uninstall", "rollback", "push"},
	"terraform": {"apply", "destroy", "import"},
	"tofu":      {"apply", "destroy", "import"},
	"rsync":     nil,
}

// ghReads are gh commands that only read.
var ghReads = map[string][]string{
	"pr":       {"list", "view", "diff", "checks", "status", "checkout"},
	"issue":    {"list", "view", "status"},
	"run":      {"list", "view", "watch", "download"},
	"release":  {"list", "view", "download"},
	"repo":     {"view", "clone", "list"},
	"workflow": {"list", "view"},
	"search":   nil,
	"auth":     {"status"},
	"browse":   nil,
	"status":   nil,
}

func outsideWords(words []string) bool {
	words = stripWrappers(words)
	if len(words) == 0 {
		return false
	}
	program := filepath.Base(strings.Trim(words[0], `"'`))
	args := words[1:]
	switch program {
	case "bash", "sh", "zsh", "dash", "fish", "eval":
		inner := strings.Join(args, " ")
		if program != "eval" {
			i := indexOf(args, "-c")
			if i < 0 {
				return false
			}
			inner = strings.Join(args[i+1:], " ")
		}
		return OutsideCommand(strings.Trim(inner, `"'`))
	case "curl":
		return hasAny(args, "-d", "--data", "--data-raw", "--data-binary", "--data-urlencode", "--json", "-F", "--form", "-T", "--upload-file") || writeMethod(args, "-X", "--request")
	case "wget":
		return hasAny(args, "--post-data", "--post-file", "--body-data", "--body-file") || writeMethod(args, "--method")
	case "http", "https", "xh", "httpie":
		for _, a := range args {
			if isWriteMethod(a) {
				return true
			}
		}
		return false
	case "gh":
		sub := subcommands(args, nil)
		if len(sub) == 0 {
			return false
		}
		reads, known := ghReads[sub[0]]
		if !known {
			// gh api, gist, secret, label, and anything new: outside.
			return !(sub[0] == "api" && !hasAny(args, "-f", "-F", "--field", "--raw-field", "--input") && !writeMethod(args, "-X", "--method"))
		}
		return reads != nil && (len(sub) < 2 || indexOf(reads, sub[1]) < 0)
	}
	if alwaysOutside[program] {
		return true
	}
	listed, known := outsideSubcommands[program]
	if !known {
		return false
	}
	if program == "rsync" {
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && strings.Contains(a, ":") {
				return true
			}
		}
		return false
	}
	sub := subcommands(args, map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--prefix": true, "--cwd": true, "--manifest-path": true, "-chdir": true})
	return len(sub) > 0 && indexOf(listed, sub[0]) >= 0
}

// subcommands are args without options; options in withValue take the next
// arg as their value.
func subcommands(args []string, withValue map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if withValue[a] {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func isAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	return i > 0 && !strings.ContainsAny(w[:i], "/-.")
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func hasAny(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f || strings.HasPrefix(a, f+"=") || len(f) == 2 && strings.HasPrefix(a, f) && !strings.HasPrefix(a, "--") {
				return true
			}
		}
	}
	return false
}

func writeMethod(args []string, flags ...string) bool {
	for i, a := range args {
		for _, f := range flags {
			switch {
			case a == f && i+1 < len(args):
				if isWriteMethod(args[i+1]) {
					return true
				}
			case strings.HasPrefix(a, f+"="):
				if isWriteMethod(strings.TrimPrefix(a, f+"=")) {
					return true
				}
			case len(f) == 2 && strings.HasPrefix(a, f) && len(a) > 2:
				if isWriteMethod(a[2:]) {
					return true
				}
			}
		}
	}
	return false
}

func isWriteMethod(m string) bool {
	switch strings.ToUpper(strings.Trim(m, `"'`)) {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// stripWrappers drops the assignments and wrappers before a command.
func stripWrappers(words []string) []string {
	for len(words) > 0 && (wrappers[words[0]] || isAssignment(words[0]) || strings.HasPrefix(words[0], "-") && len(words) > 1) {
		words = words[1:]
	}
	return words
}
