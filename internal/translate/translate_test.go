package translate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNeeded(t *testing.T) {
	const (
		english = "Update the class comment and the iPhone test: after the words are in, the box must be back."
		chinese = "明白：手机上按住说话时，让整个输入框向上浮起，离开手指，看得清里面的字。"
		mixed   = "做法：在 `ComposerView` 里调用 `layoutIfNeeded`，然后运行 go test ./..."
		code    = "```go\nfunc main() { fmt.Println(\"hello world from here\") }\n```"
		japan   = "パーサーを修正して、空の入力のテストを追加しました。"
		korean  = "파서를 수정하고 빈 입력에 대한 테스트를 추가했습니다."
	)
	cases := []struct {
		text, lang string
		want       bool
	}{
		{english, "zh-Hans", true},
		{english, "", true},
		{english, "zh-Hant", true},
		{english, "en", false},
		{chinese, "zh-Hans", false},
		{chinese, "zh-Hant", false},
		{chinese, "en", true},
		{chinese, "ja", true},
		{mixed, "zh-Hans", false},
		{mixed, "en", true},
		{japan, "zh-Hans", true},
		{japan, "ja", false},
		{korean, "zh-Hans", true},
		{korean, "ko", false},
		{"Done.", "zh-Hans", false},
		{"好的。", "en", false},
		{code, "zh-Hans", false},
		{"See https://example.com/a/b/c/d/e/f for more", "zh-Hans", false},
		{"I fixed the bug in `parse.go` and added a test that covers the empty case.", "zh-Hans", true},
	}
	for _, c := range cases {
		if got := Needed(c.text, c.lang); got != c.want {
			t.Errorf("Needed(%q, %q) = %v, want %v", c.text, c.lang, got, c.want)
		}
	}
}

func TestRun(t *testing.T) {
	var gotPrompt, gotSchema string
	gen := func(_ context.Context, project, prompt, schema string) ([]byte, error) {
		gotPrompt, gotSchema = prompt, schema
		return []byte(`{"translation":"你好，世界"}`), nil
	}
	out, err := Run(context.Background(), gen, "/tmp", "  Hello, world  ", "")
	if err != nil || out != "你好，世界" {
		t.Fatalf("Run = %q, %v", out, err)
	}
	if !strings.Contains(gotPrompt, "into Simplified Chinese.") || !strings.Contains(gotPrompt, "<text>\nHello, world\n</text>") || !strings.Contains(gotSchema, "translation") {
		t.Fatalf("prompt %q schema %q", gotPrompt, gotSchema)
	}
	if _, err := Run(context.Background(), gen, "", "你好", "ja"); err != nil || !strings.Contains(gotPrompt, "into Japanese.") {
		t.Fatalf("prompt for Japanese %q, %v", gotPrompt, err)
	}
	if _, err := Run(context.Background(), gen, "", "   ", ""); err == nil {
		t.Fatal("empty text translated")
	}
	if _, err := Run(context.Background(), gen, "", strings.Repeat("a", MaxRunes+1), ""); err == nil {
		t.Fatal("overlong text translated")
	}
	bad := func(context.Context, string, string, string) ([]byte, error) {
		return []byte(`{"translation":""}`), nil
	}
	if _, err := Run(context.Background(), bad, "", "Hello there", ""); err == nil {
		t.Fatal("empty translation accepted")
	}
	wrapped := func(context.Context, string, string, string) ([]byte, error) {
		return []byte("Here it is:\n```json\n{\"translation\": \"你好\"}\n```"), nil
	}
	if out, err := Run(context.Background(), wrapped, "", "Hello there", ""); err != nil || out != "你好" {
		t.Fatalf("wrapped JSON: %q, %v", out, err)
	}
	failing := func(context.Context, string, string, string) ([]byte, error) { return nil, errors.New("boom") }
	if _, err := Run(context.Background(), failing, "", "Hello there", ""); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestCache(t *testing.T) {
	var c Cache
	c.Put("Hello", "zh-Hans", "你好")
	if s, ok := c.Get(" Hello ", ""); !ok || s != "你好" {
		t.Fatalf("Get = %q %v", s, ok)
	}
	if _, ok := c.Get("Hello", "ja"); ok {
		t.Fatal("a translation into Chinese served for Japanese")
	}
	if got := c.Lookup([]string{Key("Hello\n", "zh-Hans"), Key("Hello", "ja")}); len(got) != 1 || got[Key("Hello", "")] != "你好" {
		t.Fatalf("Lookup = %q", got)
	}
	for i := 0; i < cacheSize+5; i++ {
		c.Put(strings.Repeat("x", i+1), "en", "y")
	}
	if _, ok := c.Get("Hello", ""); ok {
		t.Fatal("oldest translation kept beyond the cache size")
	}
	if len(c.items) != cacheSize || len(c.order) != cacheSize {
		t.Fatalf("cache holds %d/%d", len(c.items), len(c.order))
	}
}
