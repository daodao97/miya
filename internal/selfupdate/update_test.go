package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testManager(t *testing.T, data []byte) *Manager {
	t.Helper()
	pkgURL := "https://github.com/daodao97/miya/releases/download/v1.2.3/miya.tar.gz"
	hash := sha256.Sum256(data)
	manifest := Manifest{Version: "1.2.3", Protocol: 2, Packages: map[string]Package{"linux-amd64": {
		URL: pkgURL, SHA256: hex.EncodeToString(hash[:]), Size: int64(len(data)),
	}}}
	encoded, _ := json.Marshal(manifest)
	m := &Manager{platform: "linux-amd64", protocol: 2, feed: Feed, target: filepath.Join(t.TempDir(), "miya"),
		status: Status{Supported: true, Current: "1.0.0", Phase: "idle"}}
	m.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		body := data
		if r.URL.String() == Feed {
			body = encoded
		} else if r.URL.String() != pkgURL {
			t.Errorf("unexpected URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	if err := os.WriteFile(m.target, []byte("old-running-inode"), 0755); err != nil {
		t.Fatal(err)
	}
	return m
}

func await(t *testing.T, start func(func(error)) error) error {
	t.Helper()
	done := make(chan error, 1)
	if err := start(func(err error) { done <- err }); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("update worker timed out")
		return nil
	}
}

func check(t *testing.T, m *Manager) {
	t.Helper()
	if err := await(t, func(done func(error)) error { return m.StartCheck(context.Background(), done) }); err != nil {
		t.Fatal(err)
	}
}

func archive(t *testing.T, headers []*tar.Header, bodies [][]byte) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for i, h := range headers {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(bodies[i]))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(bodies[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestInstallVerifiedPackagePreservesRunningInodeAndBackup(t *testing.T) {
	// A tiny Go executable has the same module identity and ELF architecture
	// as the release; no production process, installation or network is touched.
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module miya\n\ngo 1.27\n", "main.go": "package main\nimport (\"fmt\"; \"miya/internal/selfupdate\")\nfunc main() { fmt.Print(selfupdate.Build) }\n", "internal/selfupdate/build.go": "package selfupdate\nvar Build = \"miya-release:dev:end\"\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "build", "-ldflags", "-X miya/internal/selfupdate.Build=miya-release:1.2.3:end", "-o", filepath.Join(dir, "miya"), ".")
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "GOWORK=off", "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %s: %v", out, err)
	}
	binary, err := os.ReadFile(filepath.Join(dir, "miya"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePayload(context.Background(), filepath.Join(dir, "miya"), "linux-amd64", "9.9.9"); err == nil {
		t.Fatal("mislabeled executable accepted")
	}
	data := archive(t, []*tar.Header{{Name: "miya", Mode: 0755, Typeflag: tar.TypeReg}, {Name: "release.json", Mode: 0600, Typeflag: tar.TypeReg}},
		[][]byte{binary, []byte(`{"version":"1.2.3","target":"linux-amd64"}`)})
	m := testManager(t, data)
	check(t, m)
	if m.Status().Phase != "available" {
		t.Fatalf("not offered: %+v", m.Status())
	}
	old, err := os.Open(m.target)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := await(t, func(done func(error)) error { return m.StartInstall(context.Background(), "1.2.3", done) }); err != nil {
		t.Fatal(err)
	}
	if got := m.Status(); got.Phase != "waiting" || got.Current != "1.0.0" || got.Backup == "" {
		t.Fatalf("premature running-version claim: %+v", got)
	}
	oldData, _ := io.ReadAll(old)
	backup, err := os.ReadFile(m.Status().Backup)
	if err != nil || string(oldData) != "old-running-inode" || !bytes.Equal(backup, oldData) {
		t.Fatal("running inode or rollback backup was changed")
	}
	installed, _ := os.ReadFile(m.target)
	if !bytes.Equal(installed, binary) {
		t.Fatal("incorrect installed payload")
	}
	if err := m.StartInstall(context.Background(), "1.2.3", func(error) {}); err == nil {
		t.Fatal("duplicate install accepted")
	}
}

func TestCorruptDownloadAndWrongVersionLeaveInstallationUntouched(t *testing.T) {
	m := testManager(t, []byte("untrusted bytes"))
	check(t, m)
	if err := m.StartInstall(context.Background(), "1.2.4", func(error) {}); err == nil {
		t.Fatal("unreviewed version accepted")
	}
	m.checked.Packages[m.platform] = Package{URL: m.checked.Packages[m.platform].URL, Size: 15, SHA256: strings.Repeat("00", 32)}
	err := await(t, func(done func(error)) error { return m.StartInstall(context.Background(), "1.2.3", done) })
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum not checked: %v", err)
	}
	installed, _ := os.ReadFile(m.target)
	if string(installed) != "old-running-inode" || m.Status().Backup != "" {
		t.Fatal("failed download changed installation")
	}
}

func TestNoDowngradeAndNoConcurrentCheck(t *testing.T) {
	m := testManager(t, []byte("package"))
	m.status.Current = "1.3.0"
	check(t, m)
	if m.Status().Phase != "current" {
		t.Fatal("downgrade offered")
	}
	m.mu.Lock()
	m.status.Phase = "installing"
	m.mu.Unlock()
	if err := m.StartCheck(context.Background(), func(error) {}); err == nil {
		t.Fatal("concurrent check accepted")
	}
}

func TestArchiveTraversalLinksDuplicateAndIdentityRejected(t *testing.T) {
	for _, name := range []string{"../escape", "/escape", "a\\escape"} {
		t.Run(name, func(t *testing.T) {
			data := archive(t, []*tar.Header{{Name: name, Mode: 0600, Typeflag: tar.TypeReg}}, [][]byte{[]byte("bad")})
			testUnpackRejected(t, data)
		})
	}
	for _, headers := range [][]*tar.Header{
		{{Name: "link", Linkname: "../escape", Typeflag: tar.TypeSymlink}},
		{{Name: "link", Linkname: "/tmp/escape", Typeflag: tar.TypeSymlink}},
		{{Name: "same", Typeflag: tar.TypeReg}, {Name: "same", Typeflag: tar.TypeReg}},
		{{Name: "release.json", Typeflag: tar.TypeReg}},
	} {
		bodies := make([][]byte, len(headers))
		for i := range bodies {
			bodies[i] = []byte("bad")
		}
		// Symlinks have no body.
		for i, h := range headers {
			if h.Typeflag == tar.TypeSymlink {
				bodies[i] = nil
			}
		}
		testUnpackRejected(t, archive(t, headers, bodies))
	}
}

func testUnpackRejected(t *testing.T, data []byte) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "archive")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := unpack(p, filepath.Join(dir, "unpacked"), "1.2.3", "linux-amd64"); err == nil {
		t.Fatal("unsafe archive accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Fatal("archive escaped staging directory")
	}
}

func TestReplaceRollsBackFailedRename(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "miya")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := replace(filepath.Join(dir, "missing"), target); err == nil {
		t.Fatal("missing staged payload accepted")
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatal("rollback did not restore installation")
	}
}

func TestUpdateURLs(t *testing.T) {
	for _, raw := range []string{"http://github.com/x", "https://user:secret@github.com/x", "file:///tmp/release", "https://github.com/x#fragment"} {
		if secureURL(raw) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"https://evil.example/miya", "https://github.com/other/miya/releases/download/v1/a", "https://github.com/daodao97/miya/releases/download/v1/a?redirect=evil"} {
		if validatePackage(Package{URL: raw, Size: 1, SHA256: strings.Repeat("00", 32)}) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestMacReleaseArchiveValidation(t *testing.T) {
	bundle := os.Getenv("MIYA_SELFUPDATE_MAC_BUNDLE")
	if runtime.GOOS != "darwin" || bundle == "" {
		t.Skip("set MIYA_SELFUPDATE_MAC_BUNDLE to verify a built universal application")
	}
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			out := t.TempDir()
			cmd := exec.Command("python3", "../../scripts/package-update.py", "--source", bundle, "--target", "darwin-"+arch, "--version", "0.1.0", "--out", out)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("package: %v: %s", err, output)
			}
			staged, err := unpack(filepath.Join(out, "miya-0.1.0-darwin-"+arch+".tar.gz"), filepath.Join(out, "unpacked"), "0.1.0", "darwin-"+arch)
			if err != nil {
				t.Fatal(err)
			}
			if err := validatePayload(context.Background(), staged, "darwin-"+arch, "0.1.0"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReleaseMarkerAcrossReadBoundary(t *testing.T) {
	marker := []byte("miya-release:1.2.3:end")
	data := append(bytes.Repeat([]byte("x"), (64<<10)+len(marker)-4), marker...)
	file := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if !hasReleaseMarker(file, "1.2.3") || hasReleaseMarker(file, "1.2.4") {
		t.Fatal("release marker comparison failed across a read boundary")
	}
}
