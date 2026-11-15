package approval

import "testing"

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

func TestActionKey(t *testing.T) {
	cases := map[string]string{
		"git push origin main":           "git push",
		"go test ./... && git -C x push": "git push",
		"gh pr create --fill":            "gh pr create",
		"gh -R o/r issue comment 3":      "gh issue comment",
		"curl -X POST https://x":         "curl",
		"ssh host deploy":                "ssh",
		"Edit /etc/hosts":                "Edit /etc/hosts",
	}
	for in, want := range cases {
		if got := ActionKey(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
