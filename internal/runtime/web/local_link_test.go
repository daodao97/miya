package web

import "testing"

func TestLocalLink(t *testing.T) {
	cases := []struct {
		raw  string
		port int
		path string
		ok   bool
	}{
		{"http://127.0.0.1:5173", 5173, "/", true},
		{"http://localhost:3000/app/page?x=1#top", 3000, "/app/page?x=1#top", true},
		{"http://LOCALHOST:8080/", 8080, "/", true},
		{"http://[::1]:4000/a%20b", 4000, "/a%20b", true},
		{"http://0.0.0.0:8000/docs", 8000, "/docs", true},
		{"http://localhost/", 80, "/", true},
		{"https://localhost:3000/", 0, "", false},
		{"http://example.com:3000/", 0, "", false},
		{"http://192.168.1.2:3000/", 0, "", false},
		{"http://user:pw@localhost:3000/", 0, "", false},
		{"http://localhost:0/", 0, "", false},
		{"localhost:3000", 0, "", false},
	}
	for _, c := range cases {
		port, path, ok := LocalLink(c.raw)
		if port != c.port || path != c.path || ok != c.ok {
			t.Errorf("LocalLink(%q) = %d, %q, %v; want %d, %q, %v", c.raw, port, path, ok, c.port, c.path, c.ok)
		}
	}
}
