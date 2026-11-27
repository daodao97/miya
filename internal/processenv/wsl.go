package processenv

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"miya/internal/prefs"
)

type WSLTarget struct{ Distribution, Binary, MountRoot string }

// WSLBinary is an opaque launch target, not a host filesystem path. Keeping
// the distribution in the target pins already running sessions to their host.
func WSLBinary(distribution, binary string) string {
	b, _ := json.Marshal(WSLTarget{Distribution: distribution, Binary: binary})
	return "wsl:" + base64.RawURLEncoding.EncodeToString(b)
}
func WSL(path string) (WSLTarget, bool) {
	var t WSLTarget
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(path, "wsl:"))
	ok := strings.HasPrefix(path, "wsl:") && err == nil && json.Unmarshal(b, &t) == nil && t.Binary != ""
	return t, ok
}
func IsWSL(path string) bool { _, ok := WSL(path); return ok }
func DisplayBinary(path string) string {
	if t, ok := WSL(path); ok {
		d := t.Distribution
		if d == "" {
			d = "default"
		}
		return "WSL (" + d + "): " + t.Binary
	}
	return path
}
func AgentWSL() string {
	if runtime.GOOS == "windows" {
		return prefs.Load("agentWSL")
	}
	return ""
}
func AgentBinary(name string) (string, error) {
	if d := AgentWSL(); d != "" {
		if d == "*" {
			d = ""
		}
		return WSLBinary(d, name), nil
	}
	return LookPath(name)
}

// LinuxPath maps paths passed to an agent through WSL. UNC paths retain their
// Linux root; drive paths use WSL's conventional /mnt mounts. Shell execution
// below uses wslpath instead, so custom automount roots work for CLI arguments.
func LinuxPath(distribution, p string) string { return linuxPath(distribution, "/mnt", p) }
func linuxPath(distribution, root, p string) string {
	if root == "" {
		root = "/mnt"
	}
	s := strings.ReplaceAll(p, `\`, "/")
	for _, root := range []string{"//wsl.localhost/", "//wsl$/"} {
		if strings.HasPrefix(strings.ToLower(s), root) {
			rest := s[len(root):]
			d, path, ok := strings.Cut(rest, "/")
			if ok && (distribution == "" || strings.EqualFold(d, distribution)) {
				return "/" + path
			}
			return p
		}
	}
	if len(s) >= 3 && s[1] == ':' && s[2] == '/' {
		return strings.TrimRight(root, "/") + "/" + strings.ToLower(s[:1]) + s[2:]
	}
	return p
}

const wslBootstrap = `
# Load the target account's tools without mixing profile banners into JSON RPC.
[ ! -f /etc/profile ] || . /etc/profile >/dev/null 2>&1
for profile in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
 if [ -f "$profile" ]; then . "$profile" >/dev/null 2>&1; break; fi
done
if ! command -v node >/dev/null 2>&1 && [ -s "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ]; then . "${NVM_DIR:-$HOME/.nvm}/nvm.sh" >/dev/null 2>&1; fi
# WSL imports Windows' PATH. Exclude drive-mounted entries so a missing Linux
# agent/npm never silently launches its Windows counterpart.
winroot=$(wslpath -a 'C:\' 2>/dev/null) || winroot=""
if [ -n "$winroot" ]; then
 winroot=${winroot%/}; winroot=${winroot%/*}; cleanpath=""
 IFS=: read -ra pathdirs <<< "$PATH"
 for dir in "${pathdirs[@]}"; do
  case "$dir" in "$winroot"/[a-zA-Z]/*|"$winroot"/[a-zA-Z]) continue;; esac
  cleanpath="${cleanpath:+$cleanpath:}$dir"
 done
 PATH=$cleanpath
fi
export PATH="$HOME/.local/share/miya/agents/npm/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$HOME/.bun/bin:$PATH"
args=(env)
shift
while [ "$#" -gt 0 ] && [[ "$1" =~ ^[a-zA-Z_][a-zA-Z0-9_]*= ]]; do
 key=${1%%=*}; value=${1#*=}
 case "$value" in
  [a-zA-Z]:\\*|[a-zA-Z]:/*|\\\\wsl.localhost\\*|\\\\wsl\$\\*) value=$(wslpath -a -- "$value") || exit $? ;;
 esac
 args+=("$key=$value")
 shift
done
for arg in "$@"; do
 case "$arg" in
  [a-zA-Z]:\\*|[a-zA-Z]:/*|\\\\wsl.localhost\\*|\\\\wsl\$\\*) arg=$(wslpath -a -- "$arg") || exit $? ;;
 esac
 args+=("$arg")
done
exec "${args[@]}"
`

func prepareWSL(cmd *exec.Cmd) {
	t, ok := WSL(cmd.Path)
	if !ok {
		return
	}
	if runtime.GOOS != "windows" {
		cmd.Err = errors.New("WSL execution is available only on Windows")
		return
	}
	wsl, err := LookPath("wsl.exe")
	if err != nil {
		cmd.Err = errors.New("WSL is not installed; install a Linux distribution first")
		return
	}
	args := []string{wsl}
	if t.Distribution != "" {
		args = append(args, "--distribution", t.Distribution)
	}
	if cmd.Dir != "" {
		args = append(args, "--cd", cmd.Dir)
	}
	args = append(args, "--exec", "bash", "--noprofile", "--norc", "-c", wslBootstrap, "miya-wsl", "env")
	// Inherit Linux's HOME/PATH and account files. Only explicit changes and
	// portable API/proxy settings cross the boundary, never Windows' tool PATH.
	for _, entry := range cmd.Env {
		key, val, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(key)
		if upper == "PATH" || upper == "HOME" || upper == "USERPROFILE" || strings.HasPrefix(upper, "MIYA_") {
			continue
		}
		portable := upper == "CODEX_HOME" || upper == "CLAUDE_CONFIG_DIR" || upper == "PI_CODING_AGENT_DIR" || upper == "ANTHROPIC_AUTH_TOKEN" || upper == "ANTHROPIC_MODEL" || strings.HasPrefix(upper, "DISABLE_") || strings.Contains(upper, "API_KEY") || strings.HasSuffix(upper, "_BASE_URL") || strings.HasSuffix(upper, "_PROXY") || upper == "NO_PROXY"
		if portable || os.Getenv(key) != val {
			args = append(args, key+"="+val)
		}
	}
	args = append(args, t.Binary)
	args = append(args, cmd.Args[1:]...)
	cmd.Path, cmd.Args, cmd.Dir, cmd.Env, cmd.Err = wsl, args, "", os.Environ(), nil
}

func ManagedBinDir() string {
	root := filepath.Join(filepath.Dir(prefs.SettingsPath()), "agents", "npm")
	if runtime.GOOS != "windows" {
		return filepath.Join(root, "bin")
	}
	return root
}
