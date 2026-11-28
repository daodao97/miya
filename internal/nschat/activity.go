package nschat

import (
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/chat/subagent"
	"miya/internal/nswidgets"
	"miya/internal/textutil"
)

type activityDisclosure struct {
	thread int
	batch  bool
	item   any
}

// Store only opened disclosures so a recycled cell restores the original
// turn's state when it comes back into view.
func (v View) disclosure(cx *core.Scope, item any, batch bool) (func() bool, func(core.PressEvent)) {
	key := activityDisclosure{thread: core.Untracked(cx, func() int { return v.Thread().ID }), batch: batch, item: item}
	open := core.NewSignal(cx, v.activityOpen[key])
	return open.Get, func(core.PressEvent) {
		next := !open.GetUntracked()
		if next {
			v.activityOpen[key] = true
		} else {
			delete(v.activityOpen, key)
		}
		open.Set(next)
	}
}

// runRow keeps consecutive tool events together without folding the prose
// between them.
func (v View) runRow(cx *core.Scope, run func() msgfmt.ActivityRun) core.Node {
	batch := core.NewMemo(cx, func() bool { r := run(); return r.Activity && len(r.Messages) > 1 })
	id := core.Untracked(cx, func() int { return run().ID })
	open, toggle := v.disclosure(cx, id, true)
	messages := func() []model.Message { return run().Messages }
	rows := func() core.Node {
		return core.Each(messages, msgfmt.MessageViewKey, func(itemScope *core.Scope, message func() model.Message, _ func() int) core.Node {
			return v.message(itemScope, message)
		})
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if !batch.Get() {
			return rows()
		}
		colors := nswidgets.ColorsOf(cx)
		summary := core.NewMemo(cx, func() msgfmt.ActivitySummary {
			return msgfmt.SummarizeActivities(messages())
		})
		agents := core.NewMemoFunc(cx, func() []model.SubAgent { return subagent.Latest(messages()) }, nil).Get
		label := func() string {
			text := summary.Get().Label
			if count := subagent.Counts(agents()); count != "" {
				text += " · " + count
			}
			return text
		}
		height := core.Points(28)
		if !cx.Env().Desktop() {
			height = 44
		}
		return core.View{Style: core.Style{Gap: 4, MinWidth: core.Pt(0)}, Children: []core.Node{
			base.Button{
				TestID:   "run-" + strconv.Itoa(id),
				Expanded: open, LabelFunc: label,
				Style:     core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinHeight: core.Pt(height), MinWidth: core.Pt(0)},
				StyleFunc: func() core.Style { return activityButtonStyle(cx, colors) },
				OnPress:   toggle,
				Children: []core.Node{
					core.Show{When: func() bool { return summary.Get().Running || subagent.AnyActive(agents()) },
						Then: func(*core.Scope) core.Node {
							return ui.Spinner{Size: 12, Tint: colors.Muted, TestID: "activity-run-working"}
						}},
					core.Text{ContentFunc: label, StyleFunc: func() core.Style {
						return core.Style{FontSize: 12, Color: colors.Muted(), FlexShrink: 1, MinWidth: core.Pt(0)}
					}},
					diffCounts(cx, func() model.DiffLineStats { return summary.Get().DiffStats }, false),
					activityChevron(cx, open, colors),
				},
			},
			core.Show{When: open, Then: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{Gap: 4, MinWidth: core.Pt(0)}, Children: []core.Node{rows()}}
			}},
		}}
	})
}

func activityButtonStyle(cx *core.Scope, colors nswidgets.Colors) core.Style {
	return core.Style{CornerRadius: 6, Hover: &core.Style{Background: colors.Hover()}, FocusVisible: ui.FocusRing(cx, 2)}
}

func activityChevron(cx *core.Scope, open func() bool, colors nswidgets.Colors) core.Node {
	style := core.Style{FlexShrink: 0}
	if !ui.ReduceMotion(cx) {
		style.Transition = core.Transition{Duration: 150 * time.Millisecond, Easing: core.EaseOut, Properties: core.TransitionTransform}
	}
	return core.View{Style: style, StyleFunc: func() core.Style {
		if open() {
			return core.Style{Transform: core.Transform{Rotate: 180}}
		}
		return core.Style{}
	}, Children: []core.Node{nswidgets.Icon("chevron-down", 14, colors.Muted)}}
}

func (v View) activity(cx *core.Scope, message func() model.Message) core.Node {
	colors := nswidgets.ColorsOf(cx)
	expanded, toggle := v.disclosure(cx, msgfmt.MessageViewKey(core.Untracked(cx, message)), false)
	presented := core.NewMemoFunc(cx, func() model.MessagePresentation { return msgfmt.Message(message()) }, nil).Get
	canExpand := core.NewMemo(cx, func() bool {
		p := presented()
		return strings.TrimSpace(p.Content) != "" || len(p.Plan) > 0 || len(p.Changes) > 0 || len(message().Agents) > 0
	})
	open := func() bool { return canExpand.Get() && expanded() }
	title := func() string {
		title := msgfmt.NativeText(presented().Title)
		if detail := subagent.Summary(message().Agents); detail != "" {
			title += " · " + msgfmt.NativeText(detail)
		}
		return title
	}
	running := func() bool {
		m := message()
		switch m.Status {
		case "inProgress", "in_progress", "running", "pending":
			return true
		}
		return subagent.AnyActive(m.Agents)
	}
	id := idOf(cx, message)
	header := []core.Node{
		ui.IconOf(func() *core.SVG {
			if svg, ok := nswidgets.LookupSVG(presented().Icon); ok {
				return svg
			}
			return nswidgets.SVG("terminal-2")
		}, 14, colors.Muted),
		core.Show{When: running, Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 12, Tint: colors.Muted} }},
		core.Text{ContentFunc: title, MaxLines: 1, EllipsizeMode: core.EllipsizeTail,
			Style: core.Style{FontSize: 12, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
			StyleFunc: func() core.Style {
				color := colors.Muted()
				if s := message().Status; s == "failed" || s == "declined" {
					color = colors.Failed()
				}
				return core.Style{Color: color}
			}},
		core.Show{When: canExpand.Get, Then: func(cx *core.Scope) core.Node { return activityChevron(cx, open, colors) }},
	}
	shape := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinHeight: core.Pt(28), Gap: 8, MinWidth: core.Pt(0)}
	return core.View{Style: core.Style{Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{
		core.Dynamic(func(cx *core.Scope) core.Node {
			if !canExpand.Get() {
				return core.View{TestID: "activity-" + id, Style: shape, Children: header}
			}
			return base.Button{TestID: "activity-" + id, Style: shape, Expanded: open, LabelFunc: title, Children: header,
				StyleFunc: func() core.Style { return activityButtonStyle(cx, colors) },
				OnPress:   toggle}
		}),
		core.Show{When: open, Then: func(cx *core.Scope) core.Node {
			scroll := &core.Ref{}
			follow := true
			return core.View{Style: core.Style{Padding: core.Edges{Left: 20}, MinWidth: core.Pt(0)}, Children: []core.Node{
				core.Scroll{Ref: scroll, Style: core.Style{MaxHeight: core.Pt(400)}, OnScroll: func(e core.ScrollEvent) {
					follow = e.Offset.Y+e.Size.Height >= e.ContentSize.Height-2
				}, Children: []core.Node{core.View{Style: core.Style{Gap: 8, MinWidth: core.Pt(0)}, OnLayout: func(core.Bounds) {
					if follow && core.Untracked(cx, running) {
						scroll.ScrollToEnd(false)
					}
				}, Children: []core.Node{
					core.Show{When: func() bool { return len(message().Agents) > 0 && len(presented().Plan) == 0 }, Then: func(cx *core.Scope) core.Node {
						return subAgentList(cx, func() []model.SubAgent { return message().Agents })
					}},
					core.Show{When: func() bool {
						p := presented()
						return strings.TrimSpace(p.Content) != "" || len(p.Plan) > 0 || len(p.Changes) > 0
					}, Then: func(cx *core.Scope) core.Node { return v.activityDetail(cx, presented, v.openLink(cx)) }},
				}}}},
			}}
		}},
	}}
}

func (v View) activityDetail(cx *core.Scope, p func() model.MessagePresentation, onLink func(textview.LinkPressEvent)) core.Node {
	format := core.NewMemo(cx, func() string {
		message := p()
		if len(message.Plan) > 0 {
			return "plan"
		}
		return message.Format
	})
	return core.Dynamic(func(cx *core.Scope) core.Node {
		colors := nswidgets.ColorsOf(cx)
		switch format.Get() {
		case "plan":
			return core.View{StyleFunc: func() core.Style {
				return core.Style{Gap: 8, Padding: core.All(12), Background: colors.Subtle(), CornerRadius: 8}
			}, Children: []core.Node{
				core.Each(func() []model.PresentedPlanStep { return p().Plan }, func(step model.PresentedPlanStep) string { return step.Step },
					func(cx *core.Scope, step func() model.PresentedPlanStep, _ func() int) core.Node {
						marker := func() (string, core.Color) {
							switch step().Status {
							case "completed":
								return "✓", colors.Success()
							case "in_progress", "inProgress":
								return "◉", colors.Ink()
							}
							return "○", colors.Muted()
						}
						return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 8}, Children: []core.Node{
							core.Text{ContentFunc: func() string { symbol, _ := marker(); return symbol }, StyleFunc: func() core.Style {
								_, ink := marker()
								return core.Style{FontSize: 12, Color: ink, FlexShrink: 0}
							}},
							core.Text{ContentFunc: func() string { return step().Step }, StyleFunc: func() core.Style {
								_, ink := marker()
								return core.Style{FontSize: 12, Color: ink, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}
							}},
						}}
					}),
			}}
		case "markdown":
			return ui.TextView{MarkdownFunc: func() string { return p().Content }, Styles: transcriptMarkdownStyles(cx),
				Selectable: true, OnLinkPress: onLink, OnImagePress: v.previewMarkdownImage}
		case "diff":
			return activityDiff(cx, func() string { return p().Content })
		case "files":
			return core.View{Style: core.Style{Gap: 8, MinWidth: core.Pt(0)}, Children: []core.Node{
				core.Each(func() []model.PresentedFileChange { return p().Changes }, func(file model.PresentedFileChange) string { return file.Path },
					func(cx *core.Scope, file func() model.PresentedFileChange, _ func() int) core.Node {
						return core.View{Style: core.Style{Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{
							core.Text{Selectable: core.SelectableOn, ContentFunc: func() string { return file().Path }, Style: core.Style{FontSize: 12}},
							core.Show{When: func() bool { return file().Diff != "" }, Then: func(cx *core.Scope) core.Node {
								return activityDiff(cx, func() string { return file().Diff })
							}},
						}}
					}),
			}}
		}
		return activityCode(cx, core.Text{Selectable: core.SelectableOn, ContentFunc: func() string { return msgfmt.NativeText(p().Content) }})
	})
}

// Keep fragment and unified patches readable, including the headerless
// patches some tools send while an edit is still running.
func activityDiff(cx *core.Scope, patch func() string) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return activityCode(cx, core.Text{Selectable: core.SelectableOn, SpansFunc: func() []core.Span {
		var spans []core.Span
		for line := range strings.Lines(textutil.Bounded(msgfmt.NativeText(patch()))) {
			color := colors.Muted()
			switch {
			case strings.HasPrefix(line, "+"):
				color = colors.Added()
			case strings.HasPrefix(line, "-"):
				color = colors.Failed()
			case strings.HasPrefix(line, "@@"):
				color = colors.Hunk()
			}
			spans = append(spans, core.Span{Content: line, Style: core.Style{Color: color}})
		}
		return spans
	}})
}

func activityCode(cx *core.Scope, text core.Text) core.Node {
	colors := nswidgets.ColorsOf(cx)
	text.Style = core.Style{FontFamily: nswidgets.MonoFont, FontSize: 12, LineHeight: 18, MinWidth: core.Pt(0)}
	return core.View{StyleFunc: func() core.Style {
		return core.Style{Padding: core.All(10), CornerRadius: 8, Background: colors.CodeBg(), Color: colors.CodeInk(), MinWidth: core.Pt(0)}
	}, Children: []core.Node{text}}
}
