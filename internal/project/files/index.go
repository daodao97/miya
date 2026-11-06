package files

import (
	"bufio"
	"context"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

const MaxProjectFiles = 50000

// rg respects gitignore, includes untracked files and never follows symlinks.
// The fallback keeps file references usable in packaged apps without rg.
func IndexProjectFiles(ctx context.Context, root string) ([]string, error) {
	if _, err := exec.LookPath("rg"); err == nil {
		cmd := exec.CommandContext(ctx, "rg", "--files", "--hidden", "-0", "-g", "!.git", "-g", "!node_modules", "-g", "!.quickgui", "-g", "!dist", "-g", "!vendor")
		cmd.Dir = root
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		scan := bufio.NewScanner(stdout)
		scan.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if i := strings.IndexByte(string(data), 0); i >= 0 {
				return i + 1, data[:i], nil
			}
			if atEOF && len(data) > 0 {
				return len(data), data, nil
			}
			return 0, nil, nil
		})
		var files []string
		for scan.Scan() {
			files = append(files, scan.Text())
			if len(files) >= MaxProjectFiles {
				_ = cmd.Process.Kill()
				break
			}
		}
		err = cmd.Wait()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if scan.Err() != nil {
			return nil, scan.Err()
		}
		if err == nil || len(files) > 0 {
			return files, nil
		}
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return files, nil
		}
		return nil, err
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if path != root {
				switch d.Name() {
				case ".git", "node_modules", ".quickgui", "dist", "build", "vendor", "target", ".venv":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		if len(files) >= MaxProjectFiles {
			return fs.SkipAll
		}
		return nil
	})
	return files, err
}
