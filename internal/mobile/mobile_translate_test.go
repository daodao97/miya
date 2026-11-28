package mobile

import (
	"testing"

	phoneproto "miya/internal/phone/protocol"
	"miya/internal/prefs"
	"miya/internal/translate"
	"miya/internal/widgets"
)

func TestPhoneShowsAndKeepsTheDesktopsTranslationSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewMobileApp()
	was := widgets.Translation()
	t.Cleanup(func() {
		widgets.SetTranslationSettings(translate.Settings{Off: !was.On, Auto: was.Auto, Target: was.Target.Tag, Agent: was.Agent, Model: was.Model})
	})

	desktop := translate.Settings{Auto: true, Target: "ja", Agent: "pi", Model: "sonnet"}
	m.ApplyPhoneState(phoneproto.State{Translate: true, Translation: &desktop})
	if got := widgets.Translation(); !got.Auto || got.Target.Tag != "ja" || got.Agent != "pi" || got.Model != "sonnet" {
		t.Fatalf("phone shows %+v, desktop has %+v", got, desktop)
	}

	// A desktop too old to keep them: the phone keeps its change itself.
	m.State.Translation = nil
	widgets.SetTranslationTarget("ko")
	if got := translate.LoadSettings(prefs.Load); got.Target != "ko" {
		t.Fatalf("phone kept %+v", got)
	}
}
