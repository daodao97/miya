// Package selfupdate installs Miya releases on the machine that runs the
// service. Clients can select a checked version, never a URL or a shell command.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// Set by the packaging scripts. A development build is never presented as a
// released version, even when its service protocol happens to be the same.
var Build = "miya-release:dev:end"

// The marker also survives -trimpath builds, which deliberately omit linker
// flags from debug/buildinfo. The verifier reads this same version identity
// without executing a downloaded program.
func Version() string { return strings.TrimSuffix(strings.TrimPrefix(Build, "miya-release:"), ":end") }

const Feed = "https://github.com/daodao97/miya/releases/latest/download/miya-update.json"
const maxArchive = 1 << 30

type Package struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Version  string             `json:"version"`
	Protocol int                `json:"protocol"`
	Packages map[string]Package `json:"packages"`
}

type Status struct {
	Supported bool   `json:"supported"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Phase     string `json:"phase"` // idle, checking, available, current, installing, waiting, installed, failed
	Error     string `json:"error,omitempty"`
	Backup    string `json:"backup,omitempty"`
}

// Manager serializes checks and installs across all paired clients. Its state
// survives a client disconnect; only the service's shutdown ends its work.
type Manager struct {
	mu       sync.Mutex
	status   Status
	checked  *Manifest
	target   string
	platform string
	protocol int
	feed     string
	client   *http.Client
}

func New(exe string, persistent bool, protocol int) *Manager {
	target, err := installTarget(exe, persistent)
	m := &Manager{target: target, platform: runtime.GOOS + "-" + runtime.GOARCH, protocol: protocol, feed: Feed,
		client: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("Too many update redirects")
			}
			return secureURL(req.URL.String())
		}}}
	m.status = Status{Supported: err == nil, Current: Version(), Phase: "idle"}
	if err != nil {
		m.status.Error = err.Error()
	}
	return m
}

func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) Busy() bool {
	s := m.Status()
	return s.Phase == "checking" || s.Phase == "installing"
}

func (m *Manager) ReopenFailed(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Phase, m.status.Error = "installed", err.Error()
}

func (m *Manager) begin(phase string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.status.Supported {
		return errors.New(m.status.Error)
	}
	if m.status.Phase == "checking" || m.status.Phase == "installing" || m.status.Phase == "waiting" || m.status.Phase == "installed" {
		return errors.New("A Miya update is already in progress")
	}
	m.status.Phase, m.status.Error = phase, ""
	return nil
}

func (m *Manager) fail(err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Phase, m.status.Error = "failed", err.Error()
	return err
}

func (m *Manager) StartCheck(ctx context.Context, done func(error)) error {
	if err := m.begin("checking"); err != nil {
		return err
	}
	go func() { done(m.check(ctx)) }()
	return nil
}

func (m *Manager) check(ctx context.Context) error {
	body, err := m.get(ctx, m.feed)
	if err != nil {
		return m.fail(err)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
	if err != nil {
		return m.fail(err)
	}
	var manifest Manifest
	if len(data) > 1<<20 || json.Unmarshal(data, &manifest) != nil {
		return m.fail(errors.New("Invalid Miya update manifest"))
	}
	pkg, ok := manifest.Packages[m.platform]
	if !ok || !semver.IsValid("v"+manifest.Version) || manifest.Protocol != m.protocol || validatePackage(pkg) != nil {
		return m.fail(errors.New("The release has no compatible update for this machine and client protocol"))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checked = &manifest
	m.status.Latest = manifest.Version
	m.status.Phase = "current"
	if m.status.Current == "dev" || semver.Compare("v"+manifest.Version, "v"+m.status.Current) > 0 {
		m.status.Phase = "available"
	}
	return nil
}

// Install uses the exact manifest selected by Check. A release changing while
// the user reviews it cannot silently substitute a different version.
func (m *Manager) StartInstall(ctx context.Context, version string, done func(error)) error {
	m.mu.Lock()
	if m.status.Phase != "available" || m.checked == nil || version != m.checked.Version {
		m.mu.Unlock()
		return errors.New("Check for updates again before installing")
	}
	manifest := *m.checked
	pkg := manifest.Packages[m.platform]
	m.status.Phase, m.status.Error = "installing", ""
	m.mu.Unlock()
	go func() { done(m.install(ctx, manifest, pkg)) }()
	return nil
}

func (m *Manager) install(ctx context.Context, manifest Manifest, pkg Package) error {
	work, err := os.MkdirTemp(filepath.Dir(m.target), ".miya-update-")
	if err != nil {
		return m.fail(fmt.Errorf("Cannot write beside the Miya installation: %w", err))
	}
	defer os.RemoveAll(work)
	archive, err := os.Create(filepath.Join(work, "package.tar.gz"))
	if err != nil {
		return m.fail(err)
	}
	err = m.download(ctx, pkg, archive)
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return m.fail(err)
	}
	staged, err := unpack(filepath.Join(work, "package.tar.gz"), filepath.Join(work, "unpacked"), manifest.Version, m.platform)
	if err != nil {
		return m.fail(err)
	}
	if err := validatePayload(ctx, staged, m.platform, manifest.Version); err != nil {
		return m.fail(err)
	}
	if err := ctx.Err(); err != nil {
		return m.fail(err)
	}
	backup, err := replace(staged, m.target)
	if err != nil {
		return m.fail(err)
	}
	m.mu.Lock()
	m.status.Backup, m.status.Phase = backup, "waiting"
	m.mu.Unlock()
	return nil
}

func (m *Manager) get(ctx context.Context, raw string) (io.ReadCloser, error) {
	if err := secureURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	res, err := m.client.Do(req)
	if err != nil {
		return nil, errors.New("Unable to download the Miya release")
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("Miya release download returned HTTP %d", res.StatusCode)
	}
	return res.Body, nil
}

func secureURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("Miya updates require HTTPS")
	}
	return nil
}

func validatePackage(p Package) error {
	u, err := url.Parse(p.URL)
	if err != nil || secureURL(p.URL) != nil || u.Host != "github.com" || !strings.HasPrefix(u.Path, "/daodao97/miya/releases/download/") || u.RawQuery != "" {
		return errors.New("Update package is not an official Miya release")
	}
	digest, err := hex.DecodeString(p.SHA256)
	if err != nil || len(digest) != sha256.Size || p.Size <= 0 || p.Size > maxArchive {
		return errors.New("Invalid update package checksum or size")
	}
	return nil
}

func (m *Manager) download(ctx context.Context, p Package, dst io.Writer) error {
	body, err := m.get(ctx, p.URL)
	if err != nil {
		return err
	}
	defer body.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(body, p.Size+1))
	if err != nil {
		return err
	}
	if n != p.Size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), p.SHA256) {
		return errors.New("Miya update checksum or size does not match; installation was not changed")
	}
	return nil
}
