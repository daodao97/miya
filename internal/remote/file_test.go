package remote

import (
	"context"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoteFileRequiresLivePairingAndServesReadOnly(t *testing.T) {
	desk, phone, _ := attachmentPair(t, nil)
	dir := t.TempDir()
	const name = "截图 #15.txt"
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return phone.DialFiles(ctx)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request := func(method, path string, auth, download bool, byteRange string, want int) (*http.Response, string) {
		t.Helper()
		query := url.Values{"path": {path}}
		if download {
			query.Set("download", "1")
		}
		req, err := http.NewRequest(method, "http://desktop"+RemoteFileEndpoint+"?"+query.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth {
			phone.AuthorizeRemoteFile(req)
		}
		if byteRange != "" {
			req.Header.Set("Range", byteRange)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("%s %q: status %d, body %q, err %v. Want status %d", method, path, resp.StatusCode, body, err, want)
		}
		return resp, string(body)
	}
	request(http.MethodGet, file, false, false, "", http.StatusNotFound)
	request(http.MethodPut, file, true, false, "", http.StatusMethodNotAllowed)
	request(http.MethodGet, dir, true, false, "", http.StatusNotFound)
	request(http.MethodGet, name, true, false, "", http.StatusBadRequest)
	resp, body := request(http.MethodGet, file, true, false, "bytes=1-3", http.StatusPartialContent)
	if body != "ell" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected range response: %q, %v", body, resp.Header)
	}
	resp, _ = request(http.MethodHead, file, true, true, "", http.StatusOK)
	disposition, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || params["filename"] != name {
		t.Fatalf("unexpected download headers: %v", resp.Header)
	}
	desk.Close()
	request(http.MethodGet, file, true, false, "", http.StatusNotFound)
}
