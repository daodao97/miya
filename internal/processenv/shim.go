package processenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// NodeShim accepts a package entry only when both its manifest and the actual
// shim name it. An unrelated .cmd beside node_modules is never an owned CLI.
func NodeShim(bin string) string {
	data, err := os.ReadFile(bin)
	if err != nil {
		return ""
	}
	content := strings.ToLower(strings.ReplaceAll(string(data), `\`, "/"))
	name := strings.TrimSuffix(filepath.Base(bin), filepath.Ext(bin))
	candidates := []string{"@openai/codex", "@anthropic-ai/claude-code", "@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent", "npm"}
	for _, pkg := range candidates {
		root := filepath.Join(filepath.Dir(bin), "node_modules", filepath.FromSlash(pkg))
		b, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err != nil {
			continue
		}
		var m struct {
			Name string
			Bin  json.RawMessage
		}
		if json.Unmarshal(b, &m) != nil || m.Name != pkg {
			continue
		}
		var entry string
		if json.Unmarshal(m.Bin, &entry) != nil {
			var bins map[string]string
			if json.Unmarshal(m.Bin, &bins) != nil {
				continue
			}
			entry = bins[name]
		}
		entry = strings.TrimPrefix(filepath.ToSlash(entry), "./")
		if entry == "" || strings.HasPrefix(entry, "/") || strings.Contains(entry, "..") {
			continue
		}
		if !strings.Contains(content, strings.ToLower("node_modules/"+pkg+"/"+entry)) {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(entry))
		if st, err := os.Stat(target); err == nil && !st.IsDir() {
			return target
		}
	}
	return ""
}
