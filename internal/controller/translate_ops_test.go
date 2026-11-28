package controller

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"miya/internal/agent/integrations"
	"miya/internal/prefs"

	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/translate"
)

func TestTranslationsShowWhereverTheReplyShows(t *testing.T) {
	c := &Controller{}
	translationCache().Put("Hello there, the tests pass.", "zh-Hans", "你好，测试通过了。")
	key := translate.Key("Hello there, the tests pass.", "zh-Hans")
	missing := translate.Key("Never translated", "zh-Hans")

	found, err := c.TranslationLookup([]string{key, missing})
	if err != nil || len(found) != 1 || found[key] != "你好，测试通过了。" {
		t.Fatalf("window lookup = %q, %v", found, err)
	}

	var sent remote.Frame
	ps := &phoneSession{c: c, send: func(f remote.Frame) bool { sent = f; return true }}
	if !ps.onTranslationLookup(remote.Request{Op: "translation-lookup", RequestID: "r1", Keys: []string{key, missing}}) {
		t.Fatal("lookup ended the connection")
	}
	var reply phoneproto.TranslationLookupResult
	if sent.Type != "translation-lookup" || json.Unmarshal(sent.Data, &reply) != nil || reply.RequestID != "r1" || len(reply.Translations) != 1 || reply.Translations[key] == "" {
		t.Fatalf("phone reply %s %s", sent.Type, sent.Data)
	}

	before := c.TranslationRevision
	c.translationsChanged()
	if c.TranslationRevision != before+1 {
		t.Fatalf("revision %d, want %d", c.TranslationRevision, before+1)
	}
}

func TestOneTranslationServesEveryoneAskingAtOnce(t *testing.T) {
	c := &Controller{}
	release := make(chan struct{})
	calls := 0
	old := translateWith
	translateWith = func(ctx context.Context, _ integrations.TranslateAgents, r translate.Request) (string, error) {
		calls++
		<-release
		return "合并后的译文", nil
	}
	t.Cleanup(func() { translateWith = old })
	c.Run = func(f func()) { f() }
	r := translate.Request{Text: "Asked by the desktop and the phone at once.", Lang: "zh-Hans"}
	results := make(chan string, 2)
	for range 2 {
		work, err := c.prepareTranslate(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			out, err := work()
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- out.(translate.Result).Text
		}()
	}
	key := translate.Key(r.Text, r.Lang)
	for deadline := time.Now().Add(2 * time.Second); !slices.Contains(translatingKeys(), key); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the translation under way is not shown")
		}
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	for range 2 {
		if got := <-results; got != "合并后的译文" {
			t.Fatalf("result %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("the agent was asked %d times", calls)
	}
	if slices.Contains(translatingKeys(), key) {
		t.Fatal("a finished translation still shows under way")
	}
}

func TestTranslationSettingsAreTheServicesForWindowsAndPhones(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := &Controller{}
	c.Run = func(f func()) { f() }
	c.applyTranslationSettings(translate.Settings{Auto: true, Target: "ja", Agent: "pi", Model: "sonnet"})
	if got := translate.LoadSettings(prefs.Load); got != c.Translation || got.Target != "ja" || got.Agent != "pi" || !got.Auto {
		t.Fatalf("kept %+v, service has %+v", got, c.Translation)
	}
	ps := &phoneSession{c: c, ctx: context.Background(), send: func(remote.Frame) bool { return true }}
	raw, _ := json.Marshal(translate.Settings{Off: true, Target: "en"})
	if !ps.onTranslationSettings(remote.Request{Op: "translation-settings", Translation: raw}) {
		t.Fatal("settings ended the connection")
	}
	if !c.Translation.Off || c.Translation.Target != "en" || c.Translation.Agent != "" {
		t.Fatalf("phone's change not kept: %+v", c.Translation)
	}
}
