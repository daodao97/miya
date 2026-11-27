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

func TestPackagingKeepsCatalogLanguages(t *testing.T) {
	// nsgo.yaml is written as JSON, which YAML reads as well.
	data, err := os.ReadFile("../../nsgo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Apple struct {
			Info map[string]json.RawMessage `json:"info"`
		} `json:"apple"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	catalog, err := i18ncatalog.New(os.DirFS(".."), "en")
	if err != nil {
		t.Fatal(err)
	}
	for platform, plist := range map[string]map[string]json.RawMessage{"macOS": config.Apple.Info, "iOS": config.Apple.Info} {
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
