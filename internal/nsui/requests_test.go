package nsui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/appmodel"
	"miya/internal/interaction"
	"miya/internal/nswidgets"
)

type answerHost struct{ answered []interaction.Response }

func (h *answerHost) DescribeApproval(a interaction.Request) appmodel.ApprovalView {
	return appmodel.ApprovalView{Icon: "terminal-2", Title: "Run a command", Code: a.Command, Thread: "Fix build"}
}
func (h *answerHost) Answer(_ interaction.Request, r interaction.Response) {
	h.answered = append(h.answered, r)
}
func (h *answerHost) RespondMCPAlways(interaction.Request) error { return nil }

func request(t *testing.T, raw string) interaction.Request {
	t.Helper()
	var r interaction.Request
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func showRequests(t *testing.T, h *answerHost, reqs ...interaction.Request) *nsgotest.App {
	t.Helper()
	return nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		return pendingRequests(cx, h, func() []interaction.Request { return reqs })
	}, nsgotest.Size(700, 900))
}

func TestApprovalAllowsAndDenies(t *testing.T) {
	h := &answerHost{}
	r := request(t, `{"ID":"1","Method":"item/commandExecution/requestApproval","command":"go test ./..."}`)
	app := showRequests(t, h, r)
	text := strings.Join(app.Texts(), "\n")
	if !strings.Contains(text, "Run a command") || !strings.Contains(text, "go test ./...") {
		t.Fatalf("card = %q", text)
	}
	app.Press("id:allow")
	app.Press("id:decline")
	app.Settle()
	if len(h.answered) != 2 || h.answered[0].Action != interaction.Allow || h.answered[1].Action != interaction.Decline {
		t.Fatalf("answers = %+v", h.answered)
	}
}

// Questions submit only once each has an answer, chosen or typed.
func TestQuestionsSubmitWhenAnswered(t *testing.T) {
	h := &answerHost{}
	r := request(t, `{"ID":"2","kind":"questions","questions":[
		{"id":"lang","question":"Which language?","options":[{"label":"Go"},{"label":"Rust"}]},
		{"id":"name","question":"Project name?"}]}`)
	app := showRequests(t, h, r)
	if app.Enabled("id:submit") {
		t.Fatal("submit enabled before any answer")
	}
	app.Press("label:Go")
	app.Settle()
	if app.Enabled("id:submit") {
		t.Fatal("submit enabled with one of two answers")
	}
	app.Type("label:Project name?", "miya")
	app.Settle()
	if !app.Enabled("id:submit") {
		t.Fatal("submit still disabled with both answers")
	}
	app.Press("id:submit")
	app.Settle()
	if len(h.answered) != 1 || h.answered[0].Action != interaction.Submit ||
		h.answered[0].Answers["lang"] != "Go" || h.answered[0].Answers["name"] != "miya" {
		t.Fatalf("answers = %+v", h.answered)
	}
}

// A form missing a required field shows why and submits nothing.
func TestFormValidatesBeforeSubmitting(t *testing.T) {
	h := &answerHost{}
	r := request(t, `{"ID":"3","kind":"form","serverName":"github","message":"Pick a repo",
		"requestedSchema":{"type":"object","required":["repo"],"properties":{"repo":{"type":"string","title":"Repository"}}}}`)
	app := showRequests(t, h, r)
	app.Press("id:confirm")
	app.Settle()
	if len(h.answered) != 0 {
		t.Fatalf("submitted an invalid form: %+v", h.answered)
	}
	before := strings.Join(app.Texts(), "\n")
	app.Type("label:Repository", "daodao97/miya")
	app.Press("id:confirm")
	app.Settle()
	if len(h.answered) != 1 || h.answered[0].Action != interaction.Submit || h.answered[0].Content["repo"] != "daodao97/miya" {
		t.Fatalf("answers = %+v; texts before %q", h.answered, before)
	}
}
