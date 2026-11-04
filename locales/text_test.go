package locales

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
)

func TestCatalogsCoverTheSameMessagesAndFormatArguments(t *testing.T) {
	var en, zh map[string]json.RawMessage
	for tag, target := range map[string]*map[string]json.RawMessage{"en": &en, "zh-Hans": &zh} {
		data, err := files.ReadFile(tag + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, target); err != nil {
			t.Fatal(err)
		}
	}
	if len(en) != len(zh) {
		t.Fatal("catalogs have different message counts")
	}
	verbs := regexp.MustCompile(`%%|%[+# 0-9.\-]*[vTtbcdoOxXUeEfFgGspqw]`)
	for key := range en {
		if key[0] == '@' {
			continue
		}
		if _, ok := zh[key]; !ok {
			t.Errorf("missing Chinese message: %s", key)
			continue
		}
		source, translated := Catalog.Format("en", key, nil), Catalog.Format("zh-Hans", key, nil)
		if source == "" || translated == "" {
			t.Errorf("empty message: %s", key)
		}
		if !reflect.DeepEqual(verbs.FindAllString(source, -1), verbs.FindAllString(translated, -1)) {
			t.Errorf("format arguments differ: %q / %q", source, translated)
		}
	}
	if TextIn("zh-Hans", "Settings") != "设置" || TextIn("en", "Settings") != "Settings" {
		t.Fatal("catalog lookup does not follow the requested language")
	}
	if TextIn("zh-Hans", "Unrecognized user content") != "Unrecognized user content" {
		t.Fatal("unknown text changed")
	}
}
