package webservice

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestDesktopWebPortValidationAndRedirects(t *testing.T) {
	for _, value := range []string{"", "0", "65536", "-1", "+80", "host:80", "80?host=example.com", "１２３", "99999999999999999999"} {
		if _, err := ParsePort(value); err == nil {
			t.Errorf("accepted invalid port %q", value)
		}
	}
	for _, port := range []int{1, 80, 3000, 65535} {
		if got, err := ParsePort(strconv.Itoa(port)); err != nil || got != port {
			t.Fatalf("port %d rejected", port)
		}
	}
	for input, expected := range map[string]string{
		"/assets/?q=1": "/.miya-web/80/assets/?q=1", "http://localhost/a%2Fb?q=1#x": "/.miya-web/80/a%2Fb?q=1#x", "http://127.0.0.1:80": "/.miya-web/80/", "//[::1]:80/path": "/.miya-web/80/path", "http://localhost:81/path": "http://localhost:81/path", "https://example.com/path": "https://example.com/path", "next": "next",
	} {
		if got := webServiceLocation(input, "/.miya-web/80", 80); got != expected {
			t.Errorf("redirect %q => %q, want %q", input, got, expected)
		}
	}
	handler := Server()
	for _, path := range []string{"/.miya-web/0/", "/.miya-web/65536/", "/.miya-web/example.com/", "/.miya-web/80@example.com/"} {
		if w := fileRequest(t, handler, "GET", path, nil); w.Code != 400 {
			t.Fatalf("invalid target accepted: %s (%d)", path, w.Code)
		}
	}
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
