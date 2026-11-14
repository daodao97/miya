package nsui

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/appmodel"
	"miya/internal/interaction"
	mcpaccess "miya/internal/mcp/access"
	"miya/internal/nswidgets"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/textutil"
)

// Pending requests sit above the composer as cards of one shape: a tinted
// icon and a title saying what is asked, who asks, the details, then the
// buttons at the bottom right with the default action last, as macOS
// dialogs place them. Each card is kept by its request's ID; what the user
// typed into it lives in the card's scope and goes with it.

// requestsHost is what the cards need of the controller.
type requestsHost interface {
	DescribeApproval(a interaction.Request) appmodel.ApprovalView
	Answer(a interaction.Request, reply interaction.Response)
	RespondMCPAlways(a interaction.Request) error
}

// pendingRequests lists the controller's pending requests.
func pendingRequests(cx *core.Scope, c requestsHost, approvals func() []interaction.Request) core.Node {
	return core.View{TestID: "requests", Style: core.Style{Gap: 8, MinWidth: core.Pt(0)}, Children: []core.Node{
		core.Each(approvals, func(a interaction.Request) string { return string(a.ID) },
			func(cx *core.Scope, a func() interaction.Request, _ func() int) core.Node {
				return requestCardFor(cx, c, core.Untracked(cx, a))
			}),
	}}
}

func requestCardFor(cx *core.Scope, c requestsHost, a interaction.Request) core.Node {
	switch a.Kind() {
	case interaction.Questions:
		return questionCard(cx, c, a)
	case interaction.Form:
		return formCard(cx, c, a)
	}
	return approvalCard(cx, c, a)
}

func approvalCard(cx *core.Scope, c requestsHost, a interaction.Request) core.Node {
	colors := nswidgets.ColorsOf(cx)
	v := c.DescribeApproval(a)
	subtitle := a.AgentName()
	if v.Thread != "" {
		subtitle += " · " + v.Thread
	}
	allow, deny := tr("Allow once"), tr("Deny")
	if a.Kind() == interaction.DesktopControl {
		allow, deny = tr("Done, continue"), tr("Cancel takeover")
	}
	return requestCard(cx, string(a.ID), v.Icon, colors.WarnInk, v.Title, subtitle, approvalBody(cx, v), []core.Node{
		spacer(),
		ui.Button{Text: deny, Variant: ui.ButtonSecondary, TestID: "decline", OnPress: func() { c.Answer(a, interaction.Response{Action: interaction.Decline}) }},
		ui.Button{Text: allow, TestID: "allow", OnPress: func() { c.Answer(a, interaction.Response{Action: interaction.Allow}) }},
	})
}

// approvalBody puts the agent's reason first, as prose, then exactly what
// would run or change, in monospace so it reads and copies verbatim.
func approvalBody(cx *core.Scope, v appmodel.ApprovalView) []core.Node {
	colors := nswidgets.ColorsOf(cx)
	var body []core.Node
	if v.Summary != "" {
		body = append(body, prose(v.Summary))
	}
	if v.Code != "" {
		body = append(body, codeBlock(cx, v.Code, 160))
	}
	if v.Changes != "" {
		body = append(body, codeBlock(cx, v.Changes, 220))
	}
	for _, f := range v.Facts {
		body = append(body, core.View{Style: core.Style{Direction: core.Row, Gap: 10, AlignItems: core.AlignStart, MinWidth: core.Pt(0)}, Children: []core.Node{
			core.Text{Content: f.Label, StyleFunc: func() core.Style {
				return core.Style{FontSize: 12, Color: colors.Muted(), MinWidth: core.Pt(96), FlexShrink: 0}
			}},
			core.Text{Content: f.Value, Selectable: core.SelectableOn, Style: core.Style{FontSize: 12, FlexGrow: 1, MinWidth: core.Pt(0)}},
		}})
	}
	return body
}

func questionCard(cx *core.Scope, c requestsHost, a interaction.Request) core.Node {
	colors := nswidgets.ColorsOf(cx)
	// answers by question ID; rev tells bindings they changed.
	answers := map[string]string{}
	rev := core.NewSignal(cx, 0)
	set := func(id, value string) {
		answers[id] = value
		rev.Update(func(n int) int { return n + 1 })
	}
	answered := func() int {
		rev.Get()
		n := 0
		for _, q := range a.Questions {
			if strings.TrimSpace(answers[q.ID]) != "" {
				n++
			}
		}
		return n
	}
	agent := a.AgentName()
	title := fmt.Sprintf(tr("%s has a question"), agent)
	submit, skip := tr("Submit answer"), tr("Skip question")
	if len(a.Questions) > 1 {
		title = fmt.Sprintf(tr("%s has some questions"), agent)
		submit, skip = tr("Submit answers"), tr("Skip questions")
	}
	var fields []core.Node
	for _, q := range a.Questions {
		fields = append(fields, questionField(cx, q, func() string { rev.Get(); return answers[q.ID] }, func(v string) { set(q.ID, v) }))
	}
	body := []core.Node{core.Scroll{Style: core.Style{MaxHeight: core.Pt(340)}, Children: []core.Node{
		core.View{Style: core.Style{Gap: 18, MinWidth: core.Pt(0)}, Children: fields},
	}}}
	footer := []core.Node{}
	if len(a.Questions) > 1 {
		footer = append(footer, core.Text{ContentFunc: func() string {
			return fmt.Sprintf(tr("%d of %d answered"), answered(), len(a.Questions))
		}, StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: colors.Muted()} }})
	}
	footer = append(footer, spacer(),
		ui.Button{Text: skip, Variant: ui.ButtonSecondary, TestID: "skip", OnPress: func() { c.Answer(a, interaction.Response{Action: interaction.Skip}) }},
		ui.Button{Text: submit, TestID: "submit", Disabled: func() bool { return answered() != len(a.Questions) },
			OnPress: func() {
				out := make(map[string]string, len(answers))
				for k, v := range answers {
					out[k] = v
				}
				c.Answer(a, interaction.Response{Action: interaction.Submit, Answers: out})
			}},
	)
	return requestCard(cx, string(a.ID), "message-circle", colors.Link, title, c.DescribeApproval(a).Thread, body, footer)
}

func questionField(cx *core.Scope, q interaction.Question, value func() string, set func(string)) core.Node {
	colors := nswidgets.ColorsOf(cx)
	var children []core.Node
	if q.Header != "" && q.Header != q.Question {
		children = append(children, core.Text{Content: q.Header, StyleFunc: func() core.Style {
			return core.Style{FontSize: 11, FontWeight: core.FontWeightSemibold, Color: colors.Muted()}
		}})
	}
	children = append(children, core.Text{Content: q.Question, Selectable: core.SelectableOn,
		Style: core.Style{FontSize: 13, FontWeight: core.FontWeightMedium, LineHeight: 19, MinWidth: core.Pt(0)}})
	for _, o := range q.Options {
		children = append(children, choiceRow(cx, o.Label, o.Description, func() bool { return value() == o.Label }, func() { set(o.Label) }))
	}
	placeholder := tr("Type your answer…")
	if len(q.Options) > 0 {
		placeholder = tr("You can also enter your own answer")
	}
	children = append(children, ui.Input{Label: textutil.FirstNonEmpty(q.Header, q.Question), Placeholder: placeholder, Masked: q.IsSecret,
		// A chosen option is not also typed into the answer field.
		Value: func() string {
			if v := value(); !q.HasOption(v) {
				return v
			}
			return ""
		},
		OnChange: set})
	return core.View{Style: core.Style{Gap: 8, MinWidth: core.Pt(0)}, Children: children}
}

func formCard(cx *core.Scope, c requestsHost, a interaction.Request) core.Node {
	colors := nswidgets.ColorsOf(cx)
	rememberable := mcpaccess.Rememberable(a)
	fields := map[string]string{}
	rev := core.NewSignal(cx, 0)
	formErr := core.NewSignal(cx, "")
	set := func(key, value string) {
		fields[key] = value
		rev.Update(func(n int) int { return n + 1 })
	}
	var schema phoneproto.FormSchema
	_ = json.Unmarshal(a.RequestedSchema, &schema)
	icon, title := "shield-check", tr("Confirm request")
	switch {
	case a.Mode == "url":
		icon, title = "arrow-right", tr("Continue in your browser")
	case len(schema.Properties) > 0:
		title = fmt.Sprintf(tr("%s needs some information"), textutil.FirstNonEmpty(a.ServerName, a.AgentName()))
	}
	subtitle := textutil.FirstNonEmpty(a.ServerName, a.AgentName())
	var body []core.Node
	if a.Message != "" {
		body = append(body, prose(a.Message))
	}
	if a.Mode == "url" {
		body = append(body, nswidgets.MutedText(cx, func() string { return tr("Complete this request in your browser, then confirm:") }),
			ui.Link{Text: a.URL, Href: a.URL})
	} else {
		body = append(body, formFields(cx, schema, func(k string) string { rev.Get(); return fields[k] }, set)...)
	}
	if rememberable {
		body = append(body, nswidgets.MutedText(cx, func() string {
			return tr("Allowed only in the current conversation. Permission must be granted again after the background service exits.")
		}))
	}
	body = append(body, core.Show{When: func() bool { return formErr.Get() != "" }, Then: func(cx *core.Scope) core.Node {
		return core.View{Style: core.Style{Direction: core.Row, Gap: 6, AlignItems: core.AlignStart}, Children: []core.Node{
			nswidgets.Icon("alert-circle", 14, colors.Danger),
			core.Text{ContentFunc: formErr.Get, StyleFunc: func() core.Style {
				return core.Style{FontSize: 12, Color: colors.Danger(), FlexGrow: 1, MinWidth: core.Pt(0)}
			}},
		}}
	}})
	confirm := tr("Confirm")
	if rememberable {
		confirm = tr("Allow once")
	}
	footer := []core.Node{
		nswidgets.Quiet(tr("Cancel request"), "", func() { c.Answer(a, interaction.Response{Action: interaction.Cancel}) }),
		spacer(),
		ui.Button{Text: tr("Deny"), Variant: ui.ButtonSecondary, TestID: "decline", OnPress: func() { c.Answer(a, interaction.Response{Action: interaction.Decline}) }},
	}
	if rememberable {
		footer = append(footer, ui.Button{Text: tr("Always allow in this conversation"), Variant: ui.ButtonSecondary, TestID: "always-allow",
			OnPress: func() {
				if err := c.RespondMCPAlways(a); err != nil {
					formErr.Set(err.Error())
				}
			}})
	}
	footer = append(footer, ui.Button{Text: confirm, TestID: "confirm", OnPress: func() {
		if a.Mode == "url" {
			c.Answer(a, interaction.Response{Action: interaction.Allow})
			return
		}
		// MCP forms are submitted as structured content; consent is never
		// inferred from defaults.
		content, err := phoneproto.ValidateMobileForm(a, fields)
		if err != nil {
			formErr.Set(err.Error())
			return
		}
		c.Answer(a, interaction.Response{Action: interaction.Submit, Content: content})
	}})
	return requestCard(cx, string(a.ID), icon, colors.Link, title, subtitle, body, footer)
}

// formFields draws an MCP form's fields from its schema: choices for enums
// and booleans, text for the rest. Values stay as typed; validation against
// the schema happens on confirm, as on the phone.
func formFields(cx *core.Scope, schema phoneproto.FormSchema, value func(string) string, set func(string, string)) []core.Node {
	keys := make([]string, 0, len(schema.Properties))
	for key := range schema.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []core.Node
	for _, key := range keys {
		field := schema.Properties[key]
		name := textutil.FirstNonEmpty(field.Title, key)
		heading := name
		if slices.Contains(schema.Required, key) {
			heading += tr(" · Required")
		}
		children := []core.Node{core.Text{Content: heading, Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold}}}
		if field.Description != "" {
			desc := field.Description
			children = append(children, nswidgets.MutedText(cx, func() string { return desc }))
		}
		switch {
		case len(field.Enum) > 0:
			for _, raw := range field.Enum {
				var v any
				_ = json.Unmarshal(raw, &v)
				children = append(children, choiceRow(cx, fmt.Sprint(v), "", func() bool { return value(key) == string(raw) }, func() { set(key, string(raw)) }))
			}
		case field.Type == "boolean":
			for _, o := range []struct{ value, label string }{{"true", tr("Yes")}, {"false", tr("No")}} {
				children = append(children, choiceRow(cx, o.label, "", func() bool { return value(key) == o.value }, func() { set(key, o.value) }))
			}
		default:
			children = append(children, ui.Input{Label: name, Value: func() string { return value(key) }, OnChange: func(v string) { set(key, v) }})
		}
		out = append(out, core.View{Style: core.Style{Gap: 6, MinWidth: core.Pt(0)}, Children: children})
	}
	return out
}

// requestCard is the shape every request shares.
func requestCard(cx *core.Scope, id, icon string, tint func() core.Color, title, subtitle string, body, footer []core.Node) core.Node {
	colors := nswidgets.ColorsOf(cx)
	head := []core.Node{
		core.View{StyleFunc: func() core.Style {
			return core.Style{Width: core.Pt(28), Height: core.Pt(28), FlexShrink: 0, CornerRadius: 7, Background: ui.Mix(tint(), colors.Panel(), 0.86),
				AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}
		}, Children: []core.Node{requestIcon(icon, tint)}},
		core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), Gap: 2}, Children: []core.Node{
			core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: 13, FontWeight: core.FontWeightSemibold}},
			core.Text{Content: subtitle, MaxLines: 1, StyleFunc: func() core.Style {
				return core.Style{FontSize: 11, Color: colors.Muted(), Display: displayIf(subtitle != "")}
			}},
		}},
	}
	children := []core.Node{core.View{Style: core.Style{Direction: core.Row, Gap: 10, AlignItems: core.AlignCenter}, Children: head}}
	children = append(children, body...)
	children = append(children, core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 8, AlignItems: core.AlignCenter}, Children: footer})
	return core.View{TestID: "request-" + id, StyleFunc: func() core.Style {
		return core.Style{Padding: core.Symmetric(14, 16), Gap: 12, Background: colors.Panel(), BorderWidth: 1, BorderColor: colors.Edge(), CornerRadius: 12,
			ShadowColor: core.RGBA(0, 0.06), ShadowOffset: core.Point{Y: 1}, ShadowRadius: 3}
	}, Children: children}
}

// requestIcon is the card's icon, or the shield when the name is unknown.
func requestIcon(name string, tint func() core.Color) core.Node {
	if _, ok := nswidgets.LookupSVG(name); !ok {
		name = "shield-check"
	}
	return nswidgets.Icon(name, 16, tint)
}

// choiceRow is one answer of a question or form: a radio mark, the label
// and its description, the whole row the target.
func choiceRow(cx *core.Scope, label, detail string, chosen func() bool, choose func()) core.Node {
	colors := nswidgets.ColorsOf(cx)
	text := []core.Node{core.Text{Content: label, Style: core.Style{FontSize: 13, FontWeight: core.FontWeightMedium}}}
	if detail != "" {
		text = append(text, core.Text{Content: detail, StyleFunc: func() core.Style {
			return core.Style{FontSize: 12, LineHeight: 17, Color: colors.Muted()}
		}})
	}
	return core.View{Role: core.RoleRadio, Label: label, Checked: chosen, OnPress: func(core.PressEvent) { choose() },
		StyleFunc: func() core.Style {
			s := core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 10, Padding: core.Symmetric(8, 10), CornerRadius: 8,
				BorderWidth: 1, BorderColor: colors.Line(), Cursor: core.CursorPointer, Hover: &core.Style{Background: colors.Hover()}}
			if chosen() {
				s.BorderColor, s.Background = colors.Link(), ui.Mix(colors.Link(), colors.Panel(), 0.92)
			}
			return s
		},
		Children: []core.Node{
			core.View{StyleFunc: func() core.Style {
				mark := colors.Faint()
				if chosen() {
					mark = colors.Link()
				}
				return core.Style{Width: core.Pt(16), Height: core.Pt(16), FlexShrink: 0, Margin: core.Edges{Top: 1}, CornerRadius: 8, BorderWidth: 1, BorderColor: mark,
					AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}
			}, Children: []core.Node{core.Show{When: chosen, Then: func(*core.Scope) core.Node {
				return core.View{StyleFunc: func() core.Style {
					return core.Style{Width: core.Pt(8), Height: core.Pt(8), CornerRadius: 4, Background: colors.Link()}
				}}
			}}}},
			core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), Gap: 2}, Children: text},
		}}
}

func codeBlock(cx *core.Scope, text string, maxHeight core.Points) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.Scroll{StyleFunc: func() core.Style {
		return core.Style{MaxHeight: core.Pt(maxHeight), Background: colors.Subtle(), BorderWidth: 1, BorderColor: colors.Line(), CornerRadius: 8}
	}, Children: []core.Node{core.View{Style: core.Style{Padding: core.Symmetric(8, 10)}, Children: []core.Node{
		core.Text{Content: text, Selectable: core.SelectableOn, Style: core.Style{FontFamily: nswidgets.MonoFont, FontSize: 12, LineHeight: 17}},
	}}}}
}

func prose(text string) core.Node {
	return core.Text{Content: text, Selectable: core.SelectableOn, Style: core.Style{FontSize: 13, LineHeight: 19, MinWidth: core.Pt(0)}}
}

func spacer() core.Node { return core.View{Style: core.Style{FlexGrow: 1}} }

func displayIf(on bool) core.Display {
	if on {
		return core.DisplayFlex
	}
	return core.DisplayNone
}
