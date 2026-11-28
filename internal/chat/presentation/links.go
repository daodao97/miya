package presentation

import (
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var FileLinkLocation = regexp.MustCompile(`:[0-9]+(?::[0-9]+)?$`)

func LocalFileLink(cwd, link string) (string, bool) {
	file, fileURI, ok := fileLinkPath(link)
	if !ok {
		return "", false
	}
	if !filepath.IsAbs(file) {
		if fileURI {
			return "", false
		}
		file = filepath.Join(cwd, file)
	}
	rel, err := filepath.Rel(cwd, file)
	return rel, err == nil
}

// RemoteFileLink resolves a link using the desktop workspace's path syntax.
func RemoteFileLink(cwd, link string) (string, bool) {
	raw := FileLinkLocation.ReplaceAllString(strings.TrimSpace(link), "")
	hasDrive := driveFilePath(raw)
	if hasDrive {
		// A drive-letter path looks like a URI scheme to url.Parse.
		link = "file:///" + strings.ReplaceAll(raw, "\\", "/")
	}
	file, fileURI, ok := fileLinkPath(link)
	if !ok || fileURI && !path.IsAbs(file) {
		return "", false
	}
	desktop := strings.ReplaceAll(cwd, "\\", "/")
	if hasDrive || driveFilePath(desktop) {
		if driveFilePath(strings.TrimPrefix(file, "/")) {
			return strings.TrimPrefix(file, "/"), true
		}
		if !path.IsAbs(file) {
			return path.Join(desktop, file), true
		}
	}
	if path.IsAbs(file) {
		return file, true
	}
	if path.IsAbs(cwd) {
		return path.Join(cwd, file), true
	}
	return "", false
}

func driveFilePath(file string) bool {
	return len(file) >= 3 && (file[0] >= 'A' && file[0] <= 'Z' || file[0] >= 'a' && file[0] <= 'z') &&
		file[1] == ':' && (file[2] == '/' || file[2] == '\\')
}

func fileLinkPath(link string) (file string, fileURI, ok bool) {
	link = strings.TrimSpace(link)
	if link == "" || strings.HasPrefix(link, "#") || strings.HasPrefix(link, "//") || strings.ContainsAny(link, "\\\x00\r\n") {
		return "", false, false
	}
	// Agent source citations may append :line or :line:column to the path.
	u, err := url.Parse(FileLinkLocation.ReplaceAllString(link, ""))
	if err != nil || u.Opaque != "" || u.Host != "" && !(strings.EqualFold(u.Scheme, "file") && strings.EqualFold(u.Host, "localhost")) {
		return "", false, false
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "file") {
		return "", false, false
	}
	file = FileLinkLocation.ReplaceAllString(u.Path, "")
	if file == "" || strings.ContainsAny(file, "\\\x00\r\n") {
		return "", false, false
	}
	return file, u.Scheme != "", true
}
