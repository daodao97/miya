package msgfmt

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var FileLinkLocation = regexp.MustCompile(`:[0-9]+(?::[0-9]+)?$`)

func LocalFileLink(cwd, link string) (string, bool) {
	link = strings.TrimSpace(link)
	if link == "" || strings.HasPrefix(link, "#") || strings.HasPrefix(link, "//") || strings.ContainsAny(link, "\\\x00\r\n") {
		return "", false
	}
	// Agent source citations may append :line or :line:column to the path.
	u, err := url.Parse(FileLinkLocation.ReplaceAllString(link, ""))
	if err != nil || u.Opaque != "" || u.Host != "" && !(strings.EqualFold(u.Scheme, "file") && strings.EqualFold(u.Host, "localhost")) {
		return "", false
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "file") {
		return "", false
	}
	file := FileLinkLocation.ReplaceAllString(u.Path, "")
	if file == "" || strings.ContainsAny(file, "\\\x00\r\n") {
		return "", false
	}
	if !filepath.IsAbs(file) {
		if u.Scheme != "" {
			return "", false
		}
		file = filepath.Join(cwd, file)
	}
	rel, err := filepath.Rel(cwd, file)
	return rel, err == nil
}
