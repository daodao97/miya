// Package i18ncatalog loads Miya's JSON message catalogs without a UI framework.
package i18ncatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"sync"

	"golang.org/x/text/language"
)

// Catalog is immutable after loading and safe for concurrent use.
type Catalog struct {
	source    string
	languages []string
	keys      []string
	messages  map[string]map[string]string
	matcher   language.Matcher
	mu        sync.Mutex
	matched   map[string]string
}

// New loads and checks every catalog: canonical language names, valid JSON and
// message syntax, identical message keys, and matching printf placeholders.
func New(files fs.FS, source string) (*Catalog, error) {
	if tag, err := language.Parse(source); err != nil || tag == language.Und || tag.String() != source {
		return nil, fmt.Errorf("invalid source language %q", source)
	}
	names, err := fs.Glob(files, "*.json")
	if err != nil {
		return nil, err
	}
	c := &Catalog{source: source, messages: map[string]map[string]string{}, matched: map[string]string{}}
	for _, name := range names {
		tag := strings.TrimSuffix(name, ".json")
		parsed, err := language.Parse(tag)
		if err != nil || parsed == language.Und || parsed.String() != tag {
			return nil, fmt.Errorf("%s: filename must be a canonical BCP 47 tag", name)
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, err
		}
		keys, messages, err := readMessages(data, tag)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		c.messages[tag] = messages
		if tag == source {
			c.keys = keys
		} else {
			c.languages = append(c.languages, tag)
		}
	}
	if c.messages[source] == nil {
		return nil, fmt.Errorf("missing source catalog %s.json", source)
	}
	slices.Sort(c.languages)
	c.languages = append([]string{source}, c.languages...)
	var tags []language.Tag
	for _, tag := range c.languages {
		tags = append(tags, language.MustParse(tag))
		messages := c.messages[tag]
		for _, key := range c.keys {
			text, ok := messages[key]
			if !ok {
				return nil, fmt.Errorf("%s.json: missing message %q", tag, key)
			}
			if !slices.Equal(printfArgs(text), printfArgs(c.messages[source][key])) {
				return nil, fmt.Errorf("%s.json: printf placeholders differ for %q", tag, key)
			}
		}
		for key := range messages {
			if _, ok := c.messages[source][key]; !ok {
				return nil, fmt.Errorf("%s.json: extra message %q", tag, key)
			}
		}
	}
	c.matcher = language.NewMatcher(tags)
	return c, nil
}

func readMessages(data []byte, tag string) ([]string, map[string]string, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return nil, nil, fmt.Errorf("expected a JSON object")
	}
	seen := map[string]bool{}
	messages := map[string]string{}
	var keys []string
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return nil, nil, err
		}
		key := t.(string)
		if seen[key] {
			return nil, nil, fmt.Errorf("duplicate key %q", key)
		}
		seen[key] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return nil, nil, err
		}
		if key == "@@locale" {
			var locale string
			if err := json.Unmarshal(raw, &locale); err != nil || locale != tag {
				return nil, nil, fmt.Errorf("@@locale must be %q", tag)
			}
		}
		if strings.HasPrefix(key, "@") {
			continue
		}
		if key == "" {
			return nil, nil, fmt.Errorf("empty message key")
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, nil, fmt.Errorf("%q: expected a message string", key)
		}
		text, err = parseText(text)
		if err != nil {
			return nil, nil, fmt.Errorf("%q: %w", key, err)
		}
		messages[key] = text
		keys = append(keys, key)
	}
	if _, err := d.Token(); err != nil {
		return nil, nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, nil, fmt.Errorf("unexpected data after catalog object")
	}
	return keys, messages, nil
}

// Miya's catalogs contain printf templates, not ICU arguments. Keep ICU's
// apostrophe rules (including doubled apostrophes and quoted braces), and reject
// unquoted braces so unsupported ICU arguments cannot silently ship as text.
func parseText(text string) (string, error) {
	var out strings.Builder
	quoted := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch == '\'' {
			if i+1 < len(text) && text[i+1] == '\'' {
				out.WriteByte('\'')
				i++
				continue
			}
			if quoted {
				quoted = false
				continue
			}
			if i+1 < len(text) && (text[i+1] == '{' || text[i+1] == '}') {
				quoted = true
				continue
			}
		}
		if !quoted && (ch == '{' || ch == '}') {
			return "", fmt.Errorf("unquoted brace at byte %d: ICU arguments are not supported; use printf placeholders", i)
		}
		out.WriteByte(ch)
	}
	return out.String(), nil
}

var printfVerb = regexp.MustCompile(`%%|%(?:\[[0-9]+\])?[+# 0\-]*(?:[0-9]+|\*)?(?:\.(?:[0-9]+|\*))?(?:\[[0-9]+\])?[vTtbcdoOxXUeEfFgGspqw]`)

func printfArgs(text string) []string { return printfVerb.FindAllString(text, -1) }

func (c *Catalog) Source() string      { return c.source }
func (c *Catalog) Languages() []string { return slices.Clone(c.languages) }

// Keys returns message keys in source-file order, for stable accessor generation.
func (c *Catalog) Keys() []string { return slices.Clone(c.keys) }

// Match returns the best supported language, or the source language. Different
// scripts do not match. POSIX forms reported by desktops are also accepted.
func (c *Catalog) Match(preferred []string) string {
	var tags []language.Tag
	for _, pref := range preferred {
		pref, _, _ = strings.Cut(strings.TrimSpace(pref), ".")
		if tag, err := language.Parse(strings.ReplaceAll(pref, "_", "-")); err == nil && tag != language.Und {
			tags = append(tags, tag)
		}
	}
	if len(tags) > 0 {
		if _, i, confidence := c.matcher.Match(tags...); confidence >= language.High {
			return c.languages[i]
		}
	}
	return c.source
}

func (c *Catalog) matchedLanguage(locale string) string {
	if locale == "" {
		return c.source
	}
	if c.messages[locale] != nil {
		return locale
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if tag, ok := c.matched[locale]; ok {
		return tag
	}
	tag := c.Match([]string{locale})
	if len(c.matched) < 64 {
		c.matched[locale] = tag
	}
	return tag
}

// Format returns a message in locale, falling back to the source and then key.
// args retains the old catalog API; current messages have no ICU arguments.
// Printf placeholders remain intact for the caller's fmt.Sprintf.
func (c *Catalog) Format(locale, key string, args map[string]any) string {
	if text, ok := c.messages[c.matchedLanguage(locale)][key]; ok {
		return text
	}
	if text, ok := c.messages[c.source][key]; ok {
		return text
	}
	return key
}
