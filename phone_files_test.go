package main

import (
	context "context"
	fmt "fmt"
	io "io"
	appmodel "miya/internal/appmodel"
	phoneproxy "miya/internal/phoneproxy"
	net "net"
	http "net/http"
	cookiejar "net/http/cookiejar"
	httptest "net/http/httptest"
	url "net/url"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"
	time "time"
)

func fileServerFixture(t *testing.T) (string, http.Handler) {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string]string{"index.html": "<!doctype html><link rel=stylesheet href=/assets/site.css><script src=app.js></script><h1>预览</h1>", "app.js": "document.title='loaded'", "assets/site.css": "h1{color:red}", "README.md": "# 项目\n你好", "空 格 #?.txt": "special", "<script>.txt": "safe"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler := appmodel.DesktopFileServer{Projects: func(context.Context) ([]appmodel.FileProject, error) {
		return []appmodel.FileProject{{ID: appmodel.ProjectFileID(root), Name: "Project <test>", Path: root}}, nil
	}}
	return root, handler
}

func fileRequest(t *testing.T, handler http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestDesktopFilesDirectoryLanguageFollowsClient(t *testing.T) {
	root, handler := fileServerFixture(t)
	base := "/" + appmodel.ProjectFileID(root) + "/"
	for _, tc := range []struct{ language, tag, download string }{{"zh-CN, en;q=0.8", "zh-Hans", "下载"}, {"en-GB", "en", "Download"}, {"fr", "en", "Download"}} {
		response := fileRequest(t, handler, "GET", base, map[string]string{"Accept-Language": tc.language})
		body := response.Body.String()
		if response.Code != 200 || !strings.Contains(body, `lang="`+tc.tag+`"`) || !strings.Contains(body, tc.download) || !strings.Contains(body, "Project &lt;test&gt;") || !strings.Contains(body, "README.md") {
			t.Fatalf("directory language or original names changed: %s", tc.language)
		}
	}
}

func TestDesktopFilesHTTPReadsAndDownloads(t *testing.T) {
	root, handler := fileServerFixture(t)
	base := "/" + appmodel.ProjectFileID(root) + "/"
	for _, tc := range []struct{ path, contentType, body string }{{"index.html", "text/html", "<h1>预览</h1>"}, {"assets/site.css", "text/css", "h1{color:red}"}, {"README.md", "text/plain", "# 项目"}, {url.PathEscape("空 格 #?.txt"), "text/plain", "special"}} {
		w := fileRequest(t, handler, "GET", base+tc.path, nil)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(w.Body.String(), tc.body) {
			t.Fatalf("%s: %d %v %q", tc.path, w.Code, w.Header(), w.Body.String())
		}
	}
	list := fileRequest(t, handler, "GET", base, nil)
	if !strings.Contains(list.Body.String(), "Project &lt;test&gt;") || strings.Contains(list.Body.String(), "<script>.txt") || !strings.Contains(list.Body.String(), "Download") {
		t.Fatal("directory listing is unsafe or missing download actions")
	}
	attachment := fileRequest(t, handler, "GET", base+"index.html?download=1", nil)
	if attachment.Code != 200 || !strings.Contains(attachment.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("download not offered")
	}
	head := fileRequest(t, handler, "HEAD", base+"index.html", nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD must describe file without a body")
	}
	redirect := fileRequest(t, handler, "GET", base+"assets", nil)
	if redirect.Code != 301 || redirect.Header().Get("Location") != base+"assets/" {
		t.Fatal("directory redirect incorrect")
	}
}

func TestDesktopFilesAreRootedAndReadOnly(t *testing.T) {
	root, handler := fileServerFixture(t)
	base := "/" + appmodel.ProjectFileID(root) + "/"
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("private"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.md", filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{base + "../secret.txt", base + "%2e%2e/secret.txt", base + "dir%5c..%5csecret.txt", base + "escape", "/unknown/README.md"} {
		w := fileRequest(t, handler, "GET", target, nil)
		if w.Code == 200 || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("path escaped project: %s", target)
		}
	}
	if w := fileRequest(t, handler, "GET", base+"inside", nil); w.Code != 200 {
		t.Fatal("in-project symlink was rejected")
	}
	if w := fileRequest(t, handler, "POST", base+"README.md", nil); w.Code != 405 {
		t.Fatal("file server accepted a write")
	}
	list := fileRequest(t, handler, "GET", base, nil)
	if strings.Contains(list.Body.String(), `href="escape"`) {
		t.Fatal("escaping symlink shown in directory")
	}
}

func TestDesktopFilesRangesBeyondMessageLimit(t *testing.T) {
	root, handler := fileServerFixture(t)
	full := filepath.Join(root, "large.bin")
	f, err := os.Create(full)
	if err != nil {
		t.Fatal(err)
	}
	const size = 40 << 20
	if err = f.Truncate(size); err == nil {
		_, err = f.WriteAt([]byte("TAIL"), size-4)
	}
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	w := fileRequest(t, handler, "GET", "/"+appmodel.ProjectFileID(root)+"/large.bin", map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", size-4, size-1)})
	if w.Code != 206 || w.Body.String() != "TAIL" || w.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", size-4, size-1, size) {
		t.Fatal("large file range streaming failed")
	}
}

func TestPhoneFileProxyPreservesBrowserPathsAndSession(t *testing.T) {
	root, handler := fileServerFixture(t)
	upstream := httptest.NewServer(handler)
	defer upstream.Close()
	proxy := new(phoneproxy.PhoneFileProxy)
	if err := proxy.Start(root, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", upstream.Listener.Addr().String())
	}); err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	initial, _ := url.Parse(proxy.URL)
	origin := initial.Scheme + "://" + initial.Host
	denied, err := http.Get(origin + "/")
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatal("unauthenticated loopback request accepted")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: time.Minute}
	response, err := client.Get(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.Request.URL.Path != "/" {
		t.Fatal("browser session bootstrap failed")
	}
	for target, content := range map[string]string{"/index.html": "<h1>预览</h1>", "/assets/site.css": "h1{color:red}", "/app.js": "document.title='loaded'", "/" + url.PathEscape("空 格 #?.txt"): "special"} {
		response, err = client.Get(origin + target)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(body), content) {
			t.Fatalf("browser resource failed: %s (%d) %q", target, response.StatusCode, body)
		}
	}
	response, err = client.Get(origin + "/assets")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.Request.URL.Path != "/assets/" {
		t.Fatalf("project prefix leaked into redirect: %s", response.Request.URL)
	}
	request, _ := http.NewRequest("GET", origin+"/README.md", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin file access accepted")
	}
	request, _ = http.NewRequest("GET", origin+"/README.md", nil)
	request.Host = "attacker.example"
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("foreign Host accepted")
	}
	response, err = client.Get(origin + "/../secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == 200 {
		t.Fatal("proxy normalized traversal into a readable file")
	}
	proxy.Close()
	if response, err = client.Get(origin + "/README.md"); err == nil {
		response.Body.Close()
		t.Fatal("closed preview session remained accessible")
	}
}
