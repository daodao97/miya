package processenv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// PinWSL resolves the default once when starting a session, never during
// rendering. Replies can then map Linux files back to the correct UNC share.
func PinWSL(ctx context.Context, binary string) (string, error) {
	t, ok := WSL(binary)
	if !ok {
		return binary, nil
	}
	layout, err := layoutWSL(ctx, t.Distribution)
	if err != nil {
		return "", err
	}
	t.Distribution, t.MountRoot = layout.Distribution, layout.MountRoot
	b, _ := json.Marshal(t)
	return "wsl:" + base64.RawURLEncoding.EncodeToString(b), nil
}

func WindowsPath(distribution, p string) string { return windowsPath(distribution, "/mnt", p) }
func windowsPath(distribution, root, p string) string {
	if root == "" {
		root = "/mnt"
	}
	prefix := strings.TrimRight(root, "/") + "/"
	if strings.HasPrefix(p, prefix) {
		rest := strings.TrimPrefix(p, prefix)
		if len(rest) >= 2 && rest[1] == '/' && rest[0] >= 'a' && rest[0] <= 'z' {
			return strings.ToUpper(rest[:1]) + `:\` + strings.ReplaceAll(rest[2:], "/", `\`)
		}
	}
	if strings.HasPrefix(p, "/") && distribution != "" {
		return `\\wsl.localhost\` + distribution + strings.ReplaceAll(p, "/", `\`)
	}
	return p
}

// ProtocolPaths translates only structured path fields. Prompts, tool output,
// URLs, session IDs and all other user content are preserved byte for byte as
// JSON values. UseNumber avoids rounding protocol IDs and token counters.
func ProtocolPaths(data []byte, binary string, toLinux bool) []byte {
	t, ok := WSL(binary)
	if !ok {
		return data
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&value) != nil {
		return data
	}
	pathKey := func(k string) bool {
		switch k {
		case "cwd", "path", "filePath", "file_path", "imagePath", "image_path", "workingDirectory", "writableRoots", "workspaceRoots", "roots", "paths":
			return true
		}
		return false
	}
	var visit func(any, bool) any
	visit = func(v any, isPath bool) any {
		switch x := v.(type) {
		case map[string]any:
			for k, item := range x {
				x[k] = visit(item, pathKey(k))
			}
		case []any:
			for i, item := range x {
				x[i] = visit(item, isPath)
			}
		case string:
			if isPath {
				if toLinux {
					return linuxPath(t.Distribution, t.MountRoot, x)
				}
				return windowsPath(t.Distribution, t.MountRoot, x)
			}
		}
		return v
	}
	out, err := json.Marshal(visit(value, false))
	if err != nil {
		return data
	}
	return out
}

// WriteJSON is shared by streaming clients so attachments and permission
// responses use the same path mapping as their launch arguments.
func WriteJSON(w interface{ Write([]byte) (int, error) }, v any, binary string) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = ProtocolPaths(data, binary, true)
	_, err = w.Write(append(data, '\n'))
	return err
}
