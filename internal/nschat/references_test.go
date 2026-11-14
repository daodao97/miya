package nschat

import (
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

func TestReferenceEditPreservesIdentity(t *testing.T) {
	ref := model.ThreadRef{Source: strings.Repeat("b", 32), Backend: model.AgentClaude, SessionID: "session"}
	token := model.ReferenceText(ref, "中文名称")
	raw := "before " + token + " and " + token + " after"
	for _, tc := range []struct{ label, want string }{
		{"before @中文名称 and @中文名称 after!", raw + "!"},
		{"new before @中文名称 and @中文名称 after", "new " + raw},
		{"before  and @中文名称 after", "before  and " + token + " after"},
		{"before @中文名称 AND @中文名称 after", "before " + token + " AND " + token + " after"},
		{"before @中文名称 and @中文名 after", "before " + token + " and  after"},
		{"replaced", "replaced"},
	} {
		if got := referenceEdit(raw, tc.label, nil); got != tc.want {
			t.Errorf("edit %q = %q, want %q", tc.label, got, tc.want)
		}
	}
}
func TestNativeComposerEditsReferenceLabelsAndChips(t *testing.T) {
	ref := model.ThreadRef{Source: strings.Repeat("b", 32), Backend: model.AgentCodex, SessionID: "session"}
	raw := model.ReferenceText(ref, "Previous task") + " "
	var draft *core.Signal[string]
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		draft = core.NewSignal(cx, raw)
		return View{Thread: func() model.Thread { return model.Thread{} }, Draft: draft.Get, OnDraftChange: draft.Set}
	}, nsgotest.Size(900, 700))
	if got := app.Find("id:composer").Text; got != "@Previous task " {
		t.Fatalf("displayed raw reference: %q", got)
	}
	app.Type("id:composer", "continue")
	if got := draft.GetUntracked(); got != raw+"continue" {
		t.Fatalf("identity lost after typing: %q", got)
	}
	app.Focus("id:composer")
	app.Select("id:composer", 0, len("@Previous task"))
	app.Key("mod+c")
	app.Settle()
	if app.Clipboard != model.ReferenceText(ref, "Previous task") {
		t.Fatalf("copy lost reference identity: %q", app.Clipboard)
	}
	app.Press("id:remove-reference-" + ref.URL())
	app.Settle()
	if got := draft.GetUntracked(); got != " continue" {
		t.Fatalf("remove chip = %q", got)
	}
}
