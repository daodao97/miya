package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"miya/internal/i18ncatalog"
)

func TestGeneratedCatalogIsCurrent(t *testing.T) {
	if err := run("..", true); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyPackagingKeepsCatalogOwnershipAndLanguages(t *testing.T) {
	data, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		I18n  json.RawMessage `json:"i18n"`
		MacOS struct {
			InfoPlist map[string]json.RawMessage `json:"infoPlist"`
		} `json:"macos"`
		IOS struct {
			InfoPlist map[string]json.RawMessage `json:"infoPlist"`
		} `json:"ios"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.I18n) != 0 {
		t.Fatal("MyGo packaging must not regenerate Miya's catalog")
	}
	catalog, err := i18ncatalog.New(os.DirFS(".."), "en")
	if err != nil {
		t.Fatal(err)
	}
	for platform, plist := range map[string]map[string]json.RawMessage{"macOS": config.MacOS.InfoPlist, "iOS": config.IOS.InfoPlist} {
		var languages []string
		var source string
		if err := json.Unmarshal(plist["CFBundleLocalizations"], &languages); err != nil {
			t.Fatal(platform, err)
		}
		if err := json.Unmarshal(plist["CFBundleDevelopmentRegion"], &source); err != nil {
			t.Fatal(platform, err)
		}
		if !slices.Equal(languages, catalog.Languages()) || source != catalog.Source() {
			t.Errorf("%s bundle languages do not match Miya's catalogs", platform)
		}
	}
}

func TestGenerationIsDeterministicAndCheckDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	for _, tag := range []string{"en", "zh-Hans"} {
		if err := os.WriteFile(filepath.Join(dir, tag+".json"), []byte(`{"first_message":"Hello", "HTTPStatus":"HTTP"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(dir, true); err == nil {
		t.Fatal("check accepted a missing generated file")
	}
	if err := run(dir, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "catalog_gen.go")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(dir, false); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("generation changed on second run", err)
	}
	if !bytes.Contains(first, []byte("func FirstMessage()")) || !bytes.Contains(first, []byte("func HTTPStatus()")) {
		t.Fatal("accessor naming changed")
	}
	if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, true); err == nil {
		t.Fatal("check accepted stale generated code")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "stale" {
		t.Fatal("check wrote the generated file", err)
	}
}

func TestGenerationRejectsAccessorCollisions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en.json"), []byte(`{"foo_bar":"a", "fooBar":"b"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := i18ncatalog.New(os.DirFS(dir), "en")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generate(c); err == nil {
		t.Fatal("accepted colliding accessor names")
	}
}
