package automation

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// readOnlyPrograms only read, whatever their arguments, unless an argument
// in readOnlyExceptions says otherwise.
var readOnlyPrograms = map[string]bool{"cat": true, "head": true, "tail": true, "ls": true, "pwd": true, "echo": true, "printf": true, "wc": true,
	"grep": true, "egrep": true, "rg": true, "find": true, "fd": true, "tree": true, "stat": true, "file": true, "du": true, "df": true,
	"which": true, "type": true, "date": true, "sort": true, "uniq": true, "cut": true, "tr": true, "diff": true, "cmp": true, "jq": true,
	"sed": true, "basename": true, "dirname": true, "realpath": true, "true": true, "test": true, "[": true}

// readOnlyExceptions are arguments that make a read-only program write or
// run something else.
var readOnlyExceptions = map[string][]string{
	"find": {"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls"},
	"fd":   {"-x", "--exec", "-X", "--exec-batch"},
	"sed":  {"-i", "--in-place"},
	"sort": {"-o", "--output"},
}

// readOnlySubcommands are the subcommands of these programs that only read.
var readOnlySubcommands = map[string][]string{
	"git": {"log", "show", "status", "diff", "rev-parse", "ls-files", "ls-tree", "grep", "blame", "describe", "shortlog", "cat-file", "reflog", "merge-base"},
	"go":  {"list", "version", "env", "doc"},
}

// bashCommand is the command of a Claude Bash request.
func bashCommand(ask Ask) string {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(ask.Input, &in)
	return in.Command
}

// readOnlyCommand reports whether every part of a shell command only reads:
// programs that read, without redirecting output into a file. It errs
// toward no; a read automation is refused what it is not sure of.
func readOnlyCommand(command string) bool {
	if strings.TrimSpace(command) == "" || strings.ContainsAny(command, ">") || OutsideCommand(command) {
		return false
	}
	for _, segment := range splitShell(command) {
		words := stripWrappers(strings.Fields(segment))
		if len(words) == 0 {
			continue
		}
		program := filepath.Base(strings.Trim(words[0], `"'`))
		args := words[1:]
		if readOnlyPrograms[program] {
			for _, a := range args {
				for _, bad := range readOnlyExceptions[program] {
					if a == bad || strings.HasPrefix(a, bad) && (bad == "-i" || strings.HasPrefix(a, bad+"=")) {
						return false
					}
				}
			}
			continue
		}
		if program == "gh" {
			sub := subcommands(args, map[string]bool{"-R": true, "--repo": true})
			if len(sub) < 2 || indexOf(ghReads[sub[0]], sub[1]) < 0 || sub[1] == "checkout" {
				return false
			}
			continue
		}
		reads, known := readOnlySubcommands[program]
		if !known {
			return false
		}
		sub := subcommands(args, map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true})
		if len(sub) == 0 || indexOf(reads, sub[0]) < 0 {
			return false
		}
	}
	return true
}
