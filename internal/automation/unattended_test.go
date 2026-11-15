package automation

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutsideCommand(t *testing.T) {
	outside := []string{
		"git push origin main",
		"git -C repo push",
		"go test ./... && git push",
		"FOO=1 git push --force",
		"gh pr create --fill",
		"gh pr merge 12",
		"gh issue comment 3 --body hi",
		"gh release create v1",
		"gh api repos/o/r/issues -f title=x",
		"gh api -X DELETE repos/o/r",
		"gh gist create a.txt",
		"curl -X POST https://x",
		"curl -d a=b https://x",
		"curl --json '{}' https://x",
		"curl -XPUT https://x",
		"wget --post-data=a https://x",
		"http POST example.com a=b",
		"ssh host ls",
		"rsync -a dist/ host:/srv",
		"npm publish",
		"docker push img",
		"kubectl get pods",
		"terraform apply",
		"lark-cli im +send",
		"bash -c 'git push'",
		"echo $(git push)",
		"sudo git push",
	}
	for _, c := range outside {
		if !OutsideCommand(c) {
			t.Errorf("local: %q", c)
		}
	}
	local := []string{
		"go test ./...",
		"git commit -am fix",
		"git -C repo log --oneline",
		"git status && git diff",
		"gh pr list",
		"gh pr view 12 --json title",
		"gh run view 5 --log",
		"gh api repos/o/r/pulls",
		"curl -s https://example.com",
		"curl -fsSL -o out https://x",
		"rsync -a src/ dst/",
		"npm install",
		"echo push",
		"grep -r 'git push' docs",
		"terraform plan",
		"bash scripts/test.sh",
	}
	for _, c := range local {
		if OutsideCommand(c) {
			t.Errorf("outside: %q", c)
		}
	}
}

func claudeAsk(tool string, input map[string]string) Ask {
	raw, _ := json.Marshal(input)
	return Ask{Tool: tool, Input: raw}
}

func TestDecideByAccess(t *testing.T) {
	dir := t.TempDir()
	edit := claudeAsk("Edit", map[string]string{"file_path": filepath.Join(dir, "main.go")})
	escape := claudeAsk("Write", map[string]string{"file_path": filepath.Join(dir, "..", "elsewhere.txt")})
	tests := claudeAsk("Bash", map[string]string{"command": "go test ./..."})
	push := claudeAsk("Bash", map[string]string{"command": "git push"})
	fetch := claudeAsk("WebFetch", map[string]string{"url": "https://x"})
	report := claudeAsk("mcp__miya__report_result", nil)
	codex := Ask{Command: "curl -X POST https://x"}
	type want struct{ decided, allow bool }
	cases := []struct {
		name   string
		access Access
		ask    Ask
		want   want
	}{
		{"edit in project", AccessEdit, edit, want{true, true}},
		{"edit outside project", AccessEdit, escape, want{true, false}},
		{"local command", AccessEdit, tests, want{true, true}},
		{"push under edit", AccessEdit, push, want{true, false}},
		{"web under edit", AccessEdit, fetch, want{true, false}},
		{"codex under edit", AccessEdit, codex, want{true, false}},
		{"report under read", AccessRead, report, want{true, true}},
		{"edit under read", AccessRead, edit, want{true, false}},
		{"command under read", AccessRead, tests, want{true, false}},
		{"edit under external", AccessExternal, edit, want{true, true}},
		{"push under external waits", AccessExternal, push, want{false, false}},
	}
	for _, tc := range cases {
		got, what := decide(tc.access, dir, tc.ask)
		if got.Decided != tc.want.decided || got.Allow != tc.want.allow {
			t.Errorf("%s: %+v", tc.name, got)
		}
		if got.Decided && !got.Allow && (!strings.Contains(got.Message, "Miya refused") && !strings.Contains(got.Message, "read only") || what == "") {
			t.Errorf("%s: refusal %q for %q", tc.name, got.Message, what)
		}
	}
}

// Only a running automation with an access level answers for itself, and a
// refusal is kept on the run.
func TestUnattendedAnswersForItsRun(t *testing.T) {
	w := newWakeHarness(t)
	auto := ingressAutomation(WakePolicy{})
	auto.Access = AccessEdit
	a := w.add(auto)
	if wk, err := w.rn.Ingress(a.ID, "", "", false, "ping", ""); err != nil || wk.Outcome != WakeStarted {
		t.Fatalf("%+v %v", wk, err)
	}
	thread := w.rn.Active[a.ID].Thread
	if got := w.rn.Unattended(thread, claudeAsk("Bash", map[string]string{"command": "git push"})); !got.Decided || got.Allow {
		t.Fatalf("push: %+v", got)
	}
	if refused := w.lastRun(a.ID).Refused; len(refused) != 1 || refused[0] != "git push" {
		t.Fatalf("refused: %q", refused)
	}
	if got := w.rn.Unattended(thread+100, claudeAsk("Bash", map[string]string{"command": "git push"})); got.Decided {
		t.Fatalf("another conversation was answered: %+v", got)
	}
	w.endRun(a.ID)
	if got := w.rn.Unattended(thread, claudeAsk("Bash", map[string]string{"command": "git push"})); got.Decided {
		t.Fatalf("answered after the run ended: %+v", got)
	}
	plain := w.add(ingressAutomation(WakePolicy{}))
	if _, err := w.rn.Ingress(plain.ID, "", "", false, "ping", ""); err != nil {
		t.Fatal(err)
	}
	if got := w.rn.Unattended(w.rn.Active[plain.ID].Thread, claudeAsk("Bash", map[string]string{"command": "git push"})); got.Decided {
		t.Fatalf("no access level, still answered: %+v", got)
	}
}
