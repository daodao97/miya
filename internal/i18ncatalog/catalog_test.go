package i18ncatalog

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestTextSyntax(t *testing.T) {
	for _, test := range []struct{ source, want string }{
		{"Miya''s %s", "Miya's %s"},
		{"don't change # or %02d or %%", "don't change # or %02d or %%"},
		{"'{'name'}'", "{name}"},
		{"'{name}' and ''quoted''", "{name} and 'quoted'"},
		{"'{unclosed quote", "{unclosed quote"}, // ICU auto-closes quotes at end.
		{"你好，%s", "你好，%s"},
	} {
		got, err := parseText(test.source)
		if err != nil || got != test.want {
			t.Errorf("parseText(%q) = %q, %v; want %q", test.source, got, err, test.want)
		}
	}
	for _, text := range []string{"{name}", "hello }", "{count, plural, one {file} other {files}}", "{gender, select, other {They}}"} {
		if _, err := parseText(text); err == nil {
			t.Errorf("unsupported message syntax accepted: %q", text)
		}
	}
}

func TestCatalogValidation(t *testing.T) {
	const source = `{"@@locale":"en", "message":"Hello %s %02d %%"}`
	for _, test := range []struct{ name, en, zh, failure string }{
		{"valid", source, `{"@@locale":"zh-Hans", "message":"你好 %s %02d %%"}`, ""},
		{"missing", source, `{}`, "missing message"},
		{"extra", `{}`, `{"extra":"extra"}`, "extra message"},
		{"placeholder type", source, `{"message":"Hello %d %02d %%"}`, "placeholders differ"},
		{"placeholder order", source, `{"message":"%02d %s %%"}`, "placeholders differ"},
		{"placeholder flags", source, `{"message":"%s %d %%"}`, "placeholders differ"},
		{"syntax", source, `{"message":"{broken"}`, "unquoted brace"},
		{"duplicate", `{"message":"first", "message":"second"}`, `{}`, "duplicate key"},
		{"locale", source, `{"@@locale":"en"}`, "@@locale"},
		{"value", `{"message":123}`, `{}`, "message string"},
		{"null", `{"message":null}`, `{}`, "message string"},
		{"empty key", `{"":"empty"}`, `{}`, "empty message key"},
		{"trailing", source + `{}`, `{}`, "after catalog object"},
		{"invalid JSON", `{"message":"first"`, `{}`, "end of JSON"},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := fstest.MapFS{"en.json": {Data: []byte(test.en)}, "zh-Hans.json": {Data: []byte(test.zh)}}
			_, err := New(files, "en")
			if test.failure == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.failure) {
				t.Fatalf("got %v, want error containing %q", err, test.failure)
			}
		})
	}
	for _, files := range []fstest.MapFS{
		{"zh-Hans.json": {Data: []byte(`{}`)}},
		{"en.json": {Data: []byte(`{}`)}, "zh_CN.json": {Data: []byte(`{}`)}},
	} {
		if _, err := New(files, "en"); err == nil {
			t.Fatal("accepted missing source or noncanonical filename")
		}
	}
}

func TestMatchingAndFormatting(t *testing.T) {
	c, err := New(fstest.MapFS{
		"en.json":      {Data: []byte(`{"message":"Miya''s %s", "empty":""}`)},
		"zh-Hans.json": {Data: []byte(`{"message":"中文 %s", "empty":""}`)},
	}, "en")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		preferred []string
		want      string
	}{
		{nil, "en"}, {[]string{"zh-CN"}, "zh-Hans"}, {[]string{"zh-SG"}, "zh-Hans"},
		{[]string{"zh-TW"}, "en"}, {[]string{"zh-Hant"}, "en"},
		{[]string{"en-GB"}, "en"}, {[]string{"de-DE"}, "en"},
		{[]string{"invalid!", "zh_CN.UTF-8"}, "zh-Hans"},
		{[]string{"de", "zh-CN", "en"}, "zh-Hans"},
	} {
		if got := c.Match(test.preferred); got != test.want {
			t.Errorf("Match(%v) = %s, want %s", test.preferred, got, test.want)
		}
	}
	for _, locale := range []string{"", "en", "en-GB", "de", "invalid!"} {
		if got := c.Format(locale, "message", map[string]any{"unused": 3}); got != "Miya's %s" {
			t.Errorf("Format(%q) = %q", locale, got)
		}
	}
	if c.Format("zh-CN", "message", nil) != "中文 %s" || c.Format("en", "missing", nil) != "missing" || c.Format("zh-Hans", "empty", nil) != "" {
		t.Fatal("incorrect translation, missing key, or empty message")
	}
	languages, keys := c.Languages(), c.Keys()
	languages[0], keys[0] = "broken", "broken"
	if c.Source() != "en" || c.Languages()[0] != "en" || c.Keys()[0] != "message" {
		t.Fatal("caller changed catalog internals")
	}
}
