// Package prefs keeps this Miya's small local settings: the settings file,
// the permission policy, interface language and appearance, and the order of
// its projects.
package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/appdir"
	"miya/internal/model"
)

func SettingsPath() string { return appdir.File("quickgui-settings.json") }

func LoadPermissionPolicy() string {
	path := SettingsPath()
	if path == "" {
		return "on-request"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "on-request"
	}
	var settings struct {
		PermissionPolicy string `json:"permissionPolicy"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return "on-request"
	}
	return NormalizePermissionPolicy(settings.PermissionPolicy)
}

func SavePermissionPolicy(policy string) {
	path := SettingsPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	settings["permissionPolicy"] = NormalizePermissionPolicy(policy)
	data, err := json.Marshal(settings)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

func NormalizePermissionPolicy(policy string) string {
	switch policy {
	case "on-request", "on-failure", "never":
		return policy
	default:
		return "on-request"
	}
}

func LoadLocale() string {
	path := SettingsPath()
	if path == "" {
		return "en"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "en"
	}
	var settings struct {
		Locale string `json:"locale"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return "en"
	}
	return NormalizeLocale(settings.Locale)
}

func SaveLocale(locale string) {
	path := SettingsPath()
	if path == "" {
		return
	}
	settings := map[string]string{"locale": NormalizeLocale(locale)}
	// Preserve the permission preference when adding the locale to an existing
	// settings file. Unknown future keys are intentionally left untouched.
	if data, err := os.ReadFile(path); err == nil {
		var existing map[string]any
		if json.Unmarshal(data, &existing) == nil {
			existing["locale"] = settings["locale"]
			data, _ = json.Marshal(existing)
			_ = os.WriteFile(path, data, 0600)
			return
		}
	}
	data, _ := json.Marshal(settings)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

// Keep the legacy service preference stable; device UI uses MyGo i18n.
func NormalizeLocale(_ string) string { return "en" }

// loadSetting and persistSetting read and write one string of the
// settings file, keeping the others.
func Load(key string) string {
	path := SettingsPath()
	if path == "" {
		return ""
	}
	var settings map[string]any
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	s, _ := settings[key].(string)
	return s
}

func Save(key, value string) {
	path := SettingsPath()
	if path == "" {
		return
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if value == "" {
		delete(settings, key)
	} else {
		settings[key] = value
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

// Interface language belongs to this device, independently of the service
// or workspace it connects to. MyGo owns matching, persistence and updates.
func NormalizeInterfaceLanguage(value string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-")) {
	case "en", "en-us", "en-gb":
		return "en"
	case "zh", "zh-cn", "zh-hans", "zh-hans-cn", "zh-tw", "zh-hant", "zh-hk":
		return "zh-Hans"
	default:
		return ""
	}
}

// The appearance setting: "" follows the system, else "light" or "dark".
func NormalizeAppearance(v string) string {
	if v == "light" || v == "dark" {
		return v
	}
	return ""
}

func projectOrderPath() string { return appdir.File("quickgui-project-order.json") }

func RestoreProjectOrder(projects []model.Project) []model.Project {
	var paths []string
	data, err := os.ReadFile(projectOrderPath())
	if err != nil || json.Unmarshal(data, &paths) != nil {
		return projects
	}
	byPath := make(map[string]model.Project, len(projects))
	for _, p := range projects {
		byPath[p.Path] = p
	}
	out := make([]model.Project, 0, len(projects))
	for _, path := range paths {
		if p, ok := byPath[path]; ok {
			out = append(out, p)
			delete(byPath, path)
		}
	}
	for _, p := range projects {
		if _, ok := byPath[p.Path]; ok {
			out = append(out, p)
			delete(byPath, p.Path)
		}
	}
	return out
}

func SaveProjectOrder(projects []model.Project) {
	path := projectOrderPath()
	if path == "" {
		return
	}
	paths := make([]string, 0, len(projects))
	for _, p := range projects {
		paths = append(paths, p.Path)
	}
	data, err := json.Marshal(paths)
	if err != nil {
		return
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	// Replace atomically so an interrupted write cannot lose the saved order.
	f, err := os.CreateTemp(filepath.Dir(path), ".quickgui-project-order-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(f.Name(), path)
	}
}
