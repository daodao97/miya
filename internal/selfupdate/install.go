package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func installTarget(exe string, persistent bool) (string, error) {
	if runtime.GOOS == "darwin" {
		bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
		if !strings.HasSuffix(bundle, ".app") || filepath.Base(exe) != "Miya" {
			return "", errors.New("Remote updates require an installed Miya.app")
		}
		return bundle, nil
	}
	if runtime.GOOS == "linux" && persistent {
		return exe, nil
	}
	return "", errors.New("Remote updates support macOS app bundles and Linux servers")
}

func unpack(archive, dir, version, platform string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tr := tar.NewReader(gz)
	var total int64
	seen := map[string]bool{}
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := path.Clean(h.Name)
		if count > 20000 || !filepath.IsLocal(name) || strings.Contains(name, "\\") || seen[name] {
			return "", errors.New("Unsafe or duplicate entry in Miya update")
		}
		seen[name] = true
		if h.Size < 0 || h.Size > 2*maxArchive-total {
			return "", errors.New("Miya update expands beyond the size limit")
		}
		total += h.Size
		if err := root.MkdirAll(path.Dir(name), 0755); err != nil {
			return "", err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(name, 0755)
		case tar.TypeReg:
			var out *os.File
			out, err = root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(h.Mode)&0755)
			if err == nil {
				_, err = io.Copy(out, tr)
				if closeErr := out.Close(); err == nil {
					err = closeErr
				}
			}
		case tar.TypeSymlink:
			if !filepath.IsLocal(path.Join(path.Dir(name), h.Linkname)) || path.IsAbs(h.Linkname) {
				return "", errors.New("Unsafe link in Miya update")
			}
			err = root.Symlink(h.Linkname, name)
		default:
			return "", errors.New("Unsupported entry in Miya update")
		}
		if err != nil {
			return "", err
		}
	}
	identity, err := root.Open("release.json")
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(identity, 4097))
	identity.Close()
	if err != nil {
		return "", err
	}
	var release struct{ Version, Target string }
	if len(data) > 4096 || json.Unmarshal(data, &release) != nil || release.Version != version || release.Target != platform {
		return "", errors.New("Miya update identity does not match the selected release")
	}
	payload := "miya"
	if strings.HasPrefix(platform, "darwin-") {
		payload = "Miya.app"
	}
	info, err := root.Lstat(payload)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Miya update payload is missing or is a link")
	}
	return filepath.Join(dir, payload), nil
}

func validatePayload(ctx context.Context, payload, platform, version string) error {
	binary := payload
	if strings.HasPrefix(platform, "darwin-") {
		binary = filepath.Join(payload, "Contents/MacOS/Miya")
		if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", payload).Run(); err != nil {
			return errors.New("Miya update code signature verification failed")
		}
		// Check the bundle identifier without running the downloaded executable.
		id, err := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", filepath.Join(payload, "Contents/Info.plist")).Output()
		if err != nil || strings.TrimSpace(string(id)) != "com.daodao.miya" {
			return errors.New("Miya update has a different application identifier")
		}
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return errors.New("Miya update executable is missing")
	}
	// Both release formats are Go executables. This rejects unrelated payloads
	// without executing any code from them during validation.
	bi, err := buildinfo.ReadFile(binary)
	if err != nil && strings.HasPrefix(platform, "darwin-") {
		if fat, fatErr := macho.OpenFat(binary); fatErr == nil {
			defer fat.Close()
			cpu := macho.CpuArm64
			if platform == "darwin-amd64" {
				cpu = macho.CpuAmd64
			}
			for _, a := range fat.Arches {
				if a.Cpu != cpu {
					continue
				}
				f, openErr := os.Open(binary)
				if openErr != nil {
					return openErr
				}
				bi, err = buildinfo.Read(io.NewSectionReader(f, int64(a.Offset), int64(a.Size)))
				f.Close()
				break
			}
		}
	}
	if err != nil || bi.Path != "miya" {
		return errors.New("Update executable is not Miya")
	}
	// Bind the archive identity to the actual build, so a mislabeled package
	// is rejected before replacing the installation rather than after restart.
	if !hasReleaseMarker(binary, version) {
		return errors.New("Miya update executable was built for a different release")
	}
	arch := strings.TrimPrefix(strings.TrimPrefix(platform, "linux-"), "darwin-")
	if strings.HasPrefix(platform, "linux-") {
		f, err := elf.Open(binary)
		if err != nil {
			return err
		}
		defer f.Close()
		if arch == "arm64" && f.Machine == elf.EM_AARCH64 || arch == "amd64" && f.Machine == elf.EM_X86_64 {
			return nil
		}
	} else {
		cpu := macho.CpuArm64
		if arch == "amd64" {
			cpu = macho.CpuAmd64
		}
		if fat, err := macho.OpenFat(binary); err == nil {
			defer fat.Close()
			for _, a := range fat.Arches {
				if a.Cpu == cpu {
					return nil
				}
			}
		} else if thin, err := macho.Open(binary); err == nil {
			defer thin.Close()
			if thin.Cpu == cpu {
				return nil
			}
		}
	}
	return errors.New("Miya update executable has the wrong architecture")
}

func hasReleaseMarker(binary, version string) bool {
	f, err := os.Open(binary)
	if err != nil {
		return false
	}
	defer f.Close()
	marker := []byte("miya-release:" + version + ":end")
	buf := make([]byte, (64<<10)+len(marker))
	carry := 0
	for {
		n, err := f.Read(buf[carry:])
		total := carry + n
		if bytes.Contains(buf[:total], marker) {
			return true
		}
		if err != nil {
			return false
		}
		carry = min(len(marker)-1, total)
		copy(buf, buf[total-carry:total])
	}
}

// replace keeps the old inode intact for running services and MCP processes.
// Neither an existing backup nor the running binary is overwritten in place.
func replace(staged, target string) (string, error) {
	backup := target + ".previous-" + time.Now().UTC().Format("20060102T150405.000000000")
	if err := os.Rename(target, backup); err != nil {
		return "", err
	}
	if err := os.Rename(staged, target); err != nil {
		if rollback := os.Rename(backup, target); rollback != nil {
			return "", fmt.Errorf("Update failed: %v; restore %s manually: %w", err, backup, rollback)
		}
		return "", err
	}
	return backup, nil
}

// ReopenWindows performs the same normal application quit used by the local
// installer. It never signals the service or MCP processes.
func (m *Manager) ReopenWindows(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	// The path is passed as argv, never interpolated into AppleScript source.
	script := "on run argv\n tell application (item 1 of argv) to quit\nend run"
	if err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, m.target).Run(); err != nil {
		return errors.New("The update is installed, but the desktop window could not be reopened; reopen Miya on the target computer")
	}
	return exec.CommandContext(ctx, "/usr/bin/open", "-n", m.target).Run()
}
