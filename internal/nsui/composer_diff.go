package nsui

import (
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/base/diff"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/nswidgets"
	projectgit "miya/internal/project/git"
	svcproto "miya/internal/service/protocol"
)

// The host /diff command opens a read-only review; collecting it and reading
// file sides run off the engine just as the old desktop's review panel does.
func (d *desktopComposer) diffDialog() core.Node {
	return ui.Dialog{Title: tr("Uncommitted changes"), TestID: "composer-diff", Open: d.diffOpen.Get, OnOpenChange: func(v bool, _ base.Reason) { d.diffOpen.Set(v) }, Style: core.Style{Width: core.Pt(760)}, Children: []core.Node{
		core.Show{When: d.diffOpen.Get, Then: func(cx *core.Scope) core.Node {
			project, remote := d.host.State().Cwd, d.host.State().Remote
			files := core.NewSignalFunc(cx, []projectgit.ChangedFile(nil), func(a, b []projectgit.ChangedFile) bool { return false })
			status := core.NewSignal(cx, tr("Loading changes…"))
			selected := core.NewSignal(cx, "")
			sides := core.NewSignal(cx, svcproto.HostSides{})
			loading := core.NewSignal(cx, false)
			var snapshot projectgit.DiffSnapshot
			ctx := cx.Context()
			go func() {
				var snap projectgit.DiffSnapshot
				var err error
				if remote != nil {
					err = remote.Call("host", svcproto.HostRequest{Action: "diff", Project: project, Source: projectgit.SourceUncommitted}, &snap)
				} else {
					snap, err = projectgit.CollectDiff(ctx, project, projectgit.SourceUncommitted)
				}
				cx.Post(func() {
					if err != nil {
						status.Set(err.Error())
						return
					}
					snapshot = snap
					files.Set(snap.Files)
					status.Set("")
					if len(snap.Files) == 0 {
						status.Set(tr("No changes"))
					}
				})
			}()
			open := func(file projectgit.ChangedFile) {
				selected.Set(file.Path)
				loading.Set(true)
				snap := snapshot
				go func() {
					var result svcproto.HostSides
					var err error
					if remote != nil {
						err = remote.Call("host", svcproto.HostRequest{Action: "sides", Project: project, Snapshot: snap, File: file}, &result)
					} else {
						result.Old, result.New = projectgit.FileSides(ctx, project, snap, file)
					}
					cx.Post(func() {
						if selected.Get() != file.Path {
							return
						}
						loading.Set(false)
						if err != nil {
							status.Set(err.Error())
							return
						}
						status.Set("")
						sides.Set(result)
					})
				}()
			}
			return core.Scroll{Style: core.Style{MaxHeight: core.Pt(500)}, Children: []core.Node{settingsColumn(nswidgets.MutedText(cx, status.Get),
				core.Each(files.Get, func(f projectgit.ChangedFile) string { return f.Path }, func(cx *core.Scope, get func() projectgit.ChangedFile, _ func() int) core.Node {
					path := core.Untracked(cx, func() string { return get().Path })
					return ui.Button{Text: path, Variant: ui.ButtonGhost, OnPress: func() {
						if get().Binary {
							status.Set(tr("Binary file; no diff available"))
							return
						}
						open(get())
					}}
				}),
				core.Show{When: loading.Get, Then: func(cx *core.Scope) core.Node { return ui.Spinner{Size: 14} }},
				core.Show{When: func() bool { return selected.Get() != "" && !loading.Get() }, Then: func(cx *core.Scope) core.Node {
					return diff.DiffView{TestID: "composer-diff-content", OldValue: func() string { return sides.Get().Old }, NewValue: func() string { return sides.Get().New }, Unified: true, Selectable: true}
				}},
			)}}
		}}}}
}
