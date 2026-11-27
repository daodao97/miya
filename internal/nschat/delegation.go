package nschat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/nswidgets"
	"miya/internal/textutil"
)

func (v View) delegationReceipt(cx *core.Scope, tasks []model.DelegationCard) core.Node {
	children := []core.Node{core.Text{Content: tr("Delegation results"), Style: core.Style{FontWeight: ui.Semibold}}}
	for i, task := range tasks {
		children = append(children, v.delegationCard(cx, strconv.Itoa(i), task, true))
	}
	return core.View{TestID: "delegation-receipt", Style: core.Style{Gap: 8}, Children: children}
}

func (v View) delegationCard(cx *core.Scope, key string, task model.DelegationCard, receipt bool) core.Node {
	colors := nswidgets.ColorsOf(cx)
	title := textutil.FirstNonEmpty(task.Title, tr("Delegated task"))
	status := tr(task.State)
	if task.Agent != "" {
		status = strings.TrimSuffix(fmt.Sprintf(tr("Delegated to %s"), task.Agent)+" · "+status, " · ")
	}
	if task.Read && receipt {
		status = strings.TrimPrefix(status+" · "+tr("Read by the main agent"), " · ")
	}
	heading := core.View{Style: core.Style{Direction: core.Row, Gap: 8, AlignItems: core.AlignCenter}, Children: []core.Node{
		nswidgets.Icon("stack-2", 16, colors.Muted),
		core.Text{Content: title, Style: core.Style{FontWeight: ui.Semibold, FlexGrow: 1, MinWidth: core.Pt(0)}},
	}}
	if ref, ok := model.ParseThreadURL(task.Thread); ok && v.OnOpenReference != nil {
		heading.Children = append(heading.Children, ui.Button{Text: tr("Open conversation"), Variant: ui.ButtonOutline, OnPress: func() { v.OnOpenReference(ref) }})
	}
	details := []core.Node{}
	for _, detail := range []struct{ title, content string }{
		{tr("Task brief"), task.Request}, {tr("Result"), task.Quote}, {tr("Blocked"), task.Blocked}, {tr("Details"), task.Detail},
	} {
		if detail.content != "" {
			details = append(details, core.Text{Content: detail.title, Style: core.Style{FontWeight: ui.Semibold}},
				core.Text{Content: detail.content, Selectable: core.SelectableOn})
		}
	}
	if task.Expired {
		details = append(details, core.Text{Content: tr("The retained task result has expired.")})
	}
	children := []core.Node{heading, nswidgets.MutedText(cx, func() string { return status })}
	if len(details) > 0 {
		open := core.NewSignal(cx, false)
		summary := textutil.FirstNonEmpty(task.Blocked, textutil.FirstLine(task.Quote), textutil.FirstLine(task.Detail))
		if summary != "" {
			children = append(children, core.Show{When: func() bool { return !open.Get() }, Then: func(cx *core.Scope) core.Node {
				return nswidgets.MutedText(cx, func() string { return summary })
			}})
		}
		children = append(children, ui.Button{TestID: "delegation-expand-" + key, Text: tr("Details"), Variant: ui.ButtonOutline, OnPress: func() { open.Update(func(on bool) bool { return !on }) }},
			core.Show{When: open.Get, Then: func(*core.Scope) core.Node {
				return core.View{Style: core.Style{Gap: 6}, Children: details}
			}})
	}
	return core.View{TestID: "delegation-card-" + key, StyleFunc: func() core.Style {
		return core.Style{BorderWidth: 1, BorderColor: colors.Line(), CornerRadius: 12, Padding: core.All(12), Gap: 8}
	}, Children: children}
}

func (v View) delegationMessage(cx *core.Scope, m func() model.Message) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		message := m()
		_, fallback, _ := msgfmt.DelegationCall(message)
		if len(message.Delegations) > 0 {
			fallback = message.Delegations[0]
		}
		return v.delegationCard(cx, strconv.Itoa(message.ID), fallback, false)
	})
}
