// Package worktree runs work in a separate Git worktree of a project: on its
// own branch, started from the project's HEAD, so that it cannot collide with
// edits in the project's own checkout. The worktree is a workspace of its
// own; callers register it as a project while it exists.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BranchPrefix starts every branch Miya creates for a worktree.
const BranchPrefix = "miya/"

// Worktree is one created worktree.
type Worktree struct {
	// Repo is the project's top-level directory, where merges land.
	Repo string `json:"repo"`
	// Dir is the worktree's directory: the workspace the work ran in.
	Dir    string `json:"dir"`
	Branch string `json:"branch"`
	// Base is the commit the branch started from.
	Base string `json:"base"`
}

// Changes is what a worktree holds beyond its base.
type Changes struct {
	// Commits counts commits on the branch since Base.
	Commits int `json:"commits,omitempty"`
	// Files counts changed paths not yet committed, untracked included.
	Files int `json:"files,omitempty"`
}

// Empty reports whether the work changed nothing.
func (c Changes) Empty() bool { return c.Commits == 0 && c.Files == 0 }

const timeout = 2 * time.Minute

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Never wait for an editor or credentials: there is nobody to answer.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_MERGE_AUTOEDIT=no")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// ErrNotRepository is returned for a project that is not in a Git repository
// or has no commit to start from.
var ErrNotRepository = errors.New("The project is not a Git repository with at least one commit")

// Repo is the top-level directory of the repository holding project.
func Repo(ctx context.Context, project string) (string, error) {
	top, err := git(ctx, project, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return "", ErrNotRepository
	}
	if _, err := git(ctx, top, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return "", ErrNotRepository
	}
	return filepath.Clean(top), nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Slug turns a name into a part of a path and a branch name.
func Slug(name string) string {
	s := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-.")
	if r := []rune(s); len(r) > 32 {
		s = strings.Trim(string(r[:32]), "-.")
	}
	if s == "" {
		s = "run"
	}
	return s
}

// Create makes a worktree of the repository holding project under root, on
// a new branch BranchPrefix+kind+"/"+name started from HEAD. The directory
// is named after the project and name so that the project list shows where
// it came from. A name already taken gets a numbered suffix.
func Create(ctx context.Context, project, root, kind, name string) (Worktree, error) {
	repo, err := Repo(ctx, project)
	if err != nil {
		return Worktree{}, err
	}
	base, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		return Worktree{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Worktree{}, err
	}
	name = Slug(name)
	for i := 1; ; i++ {
		if i > 100 {
			return Worktree{}, errors.New("No free worktree name for " + name)
		}
		suffix := name
		if i > 1 {
			suffix += "-" + strconv.Itoa(i)
		}
		dir := filepath.Join(root, Slug(filepath.Base(repo))+"-"+suffix)
		branch := BranchPrefix + Slug(kind) + "/" + suffix
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			continue
		}
		if _, err := git(ctx, repo, "worktree", "add", "-b", branch, dir, base); err != nil {
			return Worktree{}, err
		}
		return Worktree{Repo: repo, Dir: dir, Branch: branch, Base: base}, nil
	}
}

// Exists reports whether the worktree's directory is still there.
func (w Worktree) Exists() bool {
	info, err := os.Stat(w.Dir)
	return err == nil && info.IsDir()
}

// Changes counts what the worktree holds beyond its base.
func (w Worktree) Changes(ctx context.Context) (Changes, error) {
	var c Changes
	status, err := git(ctx, w.Dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return c, err
	}
	if status != "" {
		c.Files = len(strings.Split(status, "\n"))
	}
	count, err := git(ctx, w.Dir, "rev-list", "--count", w.Base+"..HEAD")
	if err != nil {
		return c, err
	}
	c.Commits, _ = strconv.Atoi(count)
	return c, nil
}

// Commit commits everything not yet committed in the worktree, doing
// nothing when there is none.
func (w Worktree) Commit(ctx context.Context, message string) error {
	status, err := git(ctx, w.Dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil || status == "" {
		return err
	}
	if _, err := git(ctx, w.Dir, "add", "-A"); err != nil {
		return err
	}
	_, err = git(ctx, w.Dir, "commit", "--no-verify", "-m", message)
	return err
}

// ErrMergeConflict is returned when the branch does not merge cleanly. The
// merge is aborted, so the project's checkout is as it was.
var ErrMergeConflict = errors.New("The changes conflict with the project; merge them by hand")

// Merge commits what is left in the worktree, then merges its branch into
// the branch checked out in the project. A merge that would conflict is
// aborted. Merging does not remove the worktree.
func (w Worktree) Merge(ctx context.Context, message string) error {
	if err := w.Commit(ctx, message); err != nil {
		return err
	}
	if _, err := git(ctx, w.Repo, "symbolic-ref", "--quiet", "HEAD"); err != nil {
		return errors.New("The project has no branch checked out to merge into")
	}
	if _, err := git(ctx, w.Repo, "merge", "--no-ff", "--no-edit", "-m", message, w.Branch); err != nil {
		if _, merging := git(ctx, w.Repo, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); merging == nil {
			_, _ = git(ctx, w.Repo, "merge", "--abort")
			return ErrMergeConflict
		}
		return err
	}
	return nil
}

// Remove deletes the worktree's directory and its branch. With keepBranch
// the branch stays, so a merged or kept result is not lost.
func (w Worktree) Remove(ctx context.Context, keepBranch bool) error {
	if w.Exists() {
		if _, err := git(ctx, w.Repo, "worktree", "remove", "--force", w.Dir); err != nil {
			return err
		}
	}
	_, _ = git(ctx, w.Repo, "worktree", "prune")
	if keepBranch || !strings.HasPrefix(w.Branch, BranchPrefix) {
		return nil
	}
	if _, err := git(ctx, w.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+w.Branch); err != nil {
		return nil
	}
	_, err := git(ctx, w.Repo, "branch", "-D", w.Branch)
	return err
}
