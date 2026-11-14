//go:build !mygo_ui

package integration

import (
	"context"
	"io"
	phoneproxy "miya/internal/phone/proxy"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTMLPreviewSnapshotsAndAssets(t *testing.T) {
	for _, mode := range []string{"local", "remote"} {
		t.Run(mode, func(t *testing.T) {
			root, handler := fileServerFixture(t)
			const rel = "docs/空 格 #?.HTM"
			const source = "<h1>saved</h1>"
			const edited = `<link rel="stylesheet" href="site.css"><script src="/app.js"></script><h1>unsaved</h1>`
			writeFile(t, filepath.Join(root, rel), source)
			writeFile(t, filepath.Join(root, "docs/site.css"), "h1{color:blue}")
			outside := filepath.Join(t.TempDir(), "secret.txt")
			writeFile(t, outside, "outside workspace")
			if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
				t.Fatal(err)
			}
			var proxy *phoneproxy.PhoneFileProxy
			var err error
			if mode == "local" {
				proxy, err = phoneproxy.NewLocalFileProxy(root)
			} else {
				upstream := httptest.NewServer(handler)
				t.Cleanup(upstream.Close)
				proxy = new(phoneproxy.PhoneFileProxy)
				err = proxy.Start(root, func(ctx context.Context) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", upstream.Listener.Addr().String())
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(proxy.Close)
			proxy.SetPreview(rel, edited)
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
			get := func(target string) (*http.Response, string) {
				t.Helper()
				resp, err := client.Get(target)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				return resp, string(body)
			}
			fileURL := (&url.URL{Path: "/" + rel}).String()
			resp, body := get(proxy.URL + "?path=" + url.QueryEscape(fileURL))
			if resp.StatusCode != 200 || body != edited || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || resp.Request.URL.Path != "/"+rel {
				t.Fatalf("preview failed: %d %s %q", resp.StatusCode, resp.Request.URL, body)
			}
			page := resp.Request.URL
			for resource, want := range map[string]string{"site.css": "h1{color:blue}", "/app.js": "document.title='loaded'"} {
				target, err := page.Parse(resource)
				if err != nil {
					t.Fatal(err)
				}
				if resp, body := get(target.String()); resp.StatusCode != 200 || body != want {
					t.Fatalf("asset %s: %d %q", resource, resp.StatusCode, body)
				}
			}
			proxy.SetPreview(rel, "<h1>updated</h1>")
			if _, body := get(page.String()); body != "<h1>updated</h1>" {
				t.Fatalf("snapshot not refreshed: %q", body)
			}
			if disk, err := os.ReadFile(filepath.Join(root, rel)); err != nil || string(disk) != source {
				t.Fatalf("preview wrote the source file: %q, %v", disk, err)
			}
			origin := page.Scheme + "://" + page.Host
			if resp, _ := get(origin + "/escape.txt"); resp.StatusCode == 200 {
				t.Fatal("preview read outside the workspace")
			}
			proxy.Close()
			if resp, err := client.Get(page.String()); err == nil {
				resp.Body.Close()
				t.Fatal("closed preview remained accessible")
			}
		})
	}
}
