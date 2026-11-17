package desktopui

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	phoneproxy "miya/internal/phone/proxy"
)

func TestLocalHTMLPreviewOutsideWorkspace(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	downloads := filepath.Join(base, "Downloads")
	for _, dir := range []string{workspace, downloads} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(downloads, "site.css"), []byte("h1{color:blue}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("outside preview root"), 0600); err != nil {
		t.Fatal(err)
	}
	const rel = "../Downloads/报告 #?.html"
	root, file := localHTMLPreviewPath(workspace, rel)
	if root != downloads || file != "报告 #?.html" {
		t.Fatalf("preview location = %q, %q", root, file)
	}
	proxy, err := phoneproxy.NewLocalFileProxy(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Close)
	const edited = `<link rel="stylesheet" href="site.css"><h1>unsaved report</h1>`
	proxy.SetPreview(file, edited)
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
	fileURL := (&url.URL{Path: "/" + file}).String()
	resp, body := get(proxy.URL + "?path=" + url.QueryEscape(fileURL))
	if resp.StatusCode != 200 || body != edited || resp.Request.URL.Path != "/"+file {
		t.Fatalf("preview: %d %s %q", resp.StatusCode, resp.Request.URL, body)
	}
	asset := resp.Request.URL.ResolveReference(&url.URL{Path: "site.css"})
	if resp, body := get(asset.String()); resp.StatusCode != 200 || body != "h1{color:blue}" {
		t.Fatalf("asset: %d %q", resp.StatusCode, body)
	}
	asset.Path = "/../secret.txt"
	if resp, _ := get(asset.String()); resp.StatusCode == 200 {
		t.Fatal("preview read outside the file's directory")
	}
}

func TestLocalHTMLPreviewKeepsWorkspaceAssetRoot(t *testing.T) {
	root, file := localHTMLPreviewPath("/workspace", "docs/../docs/report.html")
	if root != "/workspace" || file != "docs/report.html" {
		t.Fatalf("preview location = %q, %q", root, file)
	}
}
