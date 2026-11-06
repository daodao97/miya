//go:build miya_ios_input_test

package mobile

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/phoneapi"
	"miya/internal/uiruntime"
)

// Native input tests use the real phone page and composer, without a desktop
// connection or the ability to send a message. This entry is never shipped.
func MobileEntry() {
	m := NewMobileApp()
	m.Loaded, m.Link, m.Host = true, "miya://input-fixture", tr("Input test")
	source := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	thread := model.Thread{ID: 1, ServerID: "input-fixture", Agent: appmodel.AgentCodex, Title: tr("Native input interaction test"), HistoryLoaded: true}
	m.State = phoneapi.State{ReferenceSource: source, Projects: []phoneapi.Project{{Name: "Miya", Path: "/input-fixture", Current: true, Threads: []model.Thread{thread}}}}
	m.Router().Transition = ui.TransitionNone
	m.OpenThread(thread)
	m.ShowThread(thread)
	m.Draft = os.Getenv("MIYA_INPUT_SAMPLE")
	if m.Draft == "" {
		m.Draft = "description"
	}
	mygo.App.WhenReady(func() {
		m.win = mygo.NewWindow(mygo.WindowOptions{Title: tr("Miya Input Tests"), Width: 390, Height: 844, ExtendIntoSafeArea: true, Content: ui.View(m.ViewRoot)})
		uiruntime.SetAppWindow(m.win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
