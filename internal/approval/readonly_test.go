package approval

import "testing"

func TestReadOnlyCommand(t *testing.T) {
	for _, c := range []string{"git log --oneline -3", "cat README.md", "git -C repo status && git diff HEAD~1", "rg -n TODO internal | head -20", "find . -name '*.go' | wc -l", "gh pr view 12 --json title", "sed -n 1,20p main.go", "go list ./..."} {
		if !ReadOnlyCommand(c) {
			t.Errorf("refused: %q", c)
		}
	}
	for _, c := range []string{"echo x > README.md", "sed -i s/a/b/ main.go", "find . -name '*.tmp' -delete", "find . -exec rm {} ;", "git commit -am x", "git push", "go test ./...", "rm -rf build", "cat a | tee b", "gh pr checkout 3", "gh pr create", "npm install", "python3 script.py", "curl -X POST https://x", ""} {
		if ReadOnlyCommand(c) {
			t.Errorf("allowed: %q", c)
		}
	}
}

// rtk in front of a command changes nothing about what it does.
func TestCommandsBehindRtk(t *testing.T) {
	for _, c := range []string{"rtk git push origin main", "rtk proxy git push", "rtk gh pr create --fill", "rtk -v curl -X POST https://x", "rtk err npm publish", "rtk docker push img"} {
		if !OutsideCommand(c) {
			t.Errorf("local: %q", c)
		}
	}
	for _, c := range []string{"rtk git log --oneline -3", "rtk read README.md", "rtk grep -n TODO .", "rtk ls", "rtk git status && rtk git diff", "rtk gh pr view 3"} {
		if !ReadOnlyCommand(c) {
			t.Errorf("not read-only: %q", c)
		}
	}
	for _, c := range []string{"rtk git commit -am x", "rtk test go test ./...", "rtk npm install"} {
		if ReadOnlyCommand(c) {
			t.Errorf("read-only: %q", c)
		}
	}
	if k := ActionKey("rtk git push origin main"); k != "git push" {
		t.Errorf("key %q", k)
	}
}
