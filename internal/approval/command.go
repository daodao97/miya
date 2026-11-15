package approval

// Package approval classifies what an agent asks to do: whether a command
// reaches outside the computer, only reads, and what kind of action it is.
// The automations and ordinary conversations share it (docs/action-review.md).

import (
	"path/filepath"
	"strings"
)

// ActionKey is the kind of an action: the program and subcommand of its
// first part that reaches outside, such as "git push" or "gh pr create".
// An action it cannot read is its own kind.
func ActionKey(action string) string {
	for _, segment := range splitShell(action) {
		words := strings.Fields(segment)
		if !outsideWords(words) {
			continue
		}
		words = stripWrappers(unquote(words))
		program := filepath.Base(words[0])
		n := 1
		switch {
		case program == "gh":
			n = 2
		case alwaysOutside[program] || program == "curl" || program == "wget" || program == "rsync":
			n = 0
		case program == "bash" || program == "sh" || program == "zsh" || program == "eval":
			return strings.TrimSpace(action)
		}
		sub := subcommands(words[1:], map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "-R": true, "--repo": true})
		return strings.Join(append([]string{program}, sub[:min(n, len(sub))]...), " ")
	}
	return strings.TrimSpace(action)
}

// OutsidePart is the parts of a shell command that may act outside the
// computer, as OutsideCommand reads them, joined by " && ", or "" when
// none does.
func OutsidePart(command string) string {
	var parts []string
	for _, segment := range splitShell(command) {
		if outsideWords(strings.Fields(segment)) {
			parts = append(parts, trimStray(strings.TrimSpace(segment)))
		}
	}
	return strings.Join(parts, " && ")
}

// OutsideCommand reports whether a shell command may act outside the
// computer: push, publish, open or comment on pull requests and issues,
// send messages or write over the network. It errs toward outside, and is
// no security boundary: a command can hide what it does.
func OutsideCommand(command string) bool {
	return OutsidePart(command) != ""
}

// splitShell splits a command line at the operators that start another
// command, including command substitution.
func splitShell(command string) []string {
	replacer := strings.NewReplacer("\n", ";", "&&", ";", "||", ";", "|", ";", "&", ";", "$(", ";", "`", ";", "(", ";", ")", ";", "{", ";", "}", ";")
	return strings.Split(replacer.Replace(command), ";")
}

// unquote trims the quotes around each word and drops words left empty.
// splitShell does not read quotes, so a segment cut from inside a quoted
// command keeps a stray one: in bash -c "git add . && git push" the second
// segment is git push", which must still read as git push.
func unquote(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if w = strings.Trim(w, `"'`); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// trimStray drops a quote left unmatched at either end of a segment
// splitShell cut from inside a quoted command.
func trimStray(s string) string {
	for _, q := range []string{`"`, `'`} {
		if strings.Count(s, q)%2 == 1 {
			s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, q), q))
		}
	}
	return s
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
	words = stripWrappers(unquote(words))
	if len(words) == 0 {
		return false
	}
	program := filepath.Base(words[0])
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

// stripWrappers drops the assignments and wrappers before a command, and
// sees through rtk, the output filter some setups put in front of every
// command: rtk git push is git push.
func stripWrappers(words []string) []string {
	for {
		for len(words) > 0 && (wrappers[words[0]] || isAssignment(words[0]) || strings.HasPrefix(words[0], "-") && len(words) > 1) {
			words = words[1:]
		}
		if len(words) == 0 || filepath.Base(strings.Trim(words[0], `"'`)) != "rtk" {
			return words
		}
		words = words[1:]
		for len(words) > 1 && strings.HasPrefix(words[0], "-") {
			words = words[1:]
		}
		if len(words) == 0 {
			return words
		}
		switch words[0] {
		case "proxy", "err", "test", "summary":
			words = words[1:]
		case "read", "smart", "json", "log":
			words = append([]string{"cat"}, words[1:]...)
		}
	}
}
