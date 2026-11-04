//go:build miya_ios_input_test

package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Native input tests use the real phone page and composer, without a desktop
// connection or the ability to send a message. This entry is never shipped.
func mobileEntry() {
	m := newMobileApp()
	m.loaded, m.link, m.host = true, "miya://input-fixture", tr("Input test")
	source := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	thread := Thread{ID: 1, ServerID: "input-fixture", Agent: agentCodex, Title: tr("Native input interaction test"), HistoryLoaded: true}
	m.state = phoneState{ReferenceSource: source, Projects: []phoneProject{{Name: "Miya", Path: "/input-fixture", Current: true, Threads: []Thread{thread}}}}
	m.router().Transition = ui.TransitionNone
	m.openThread(thread)
	m.showThread(thread)
	m.draft = os.Getenv("MIYA_INPUT_SAMPLE")
	if m.draft == "" {
		m.draft = "description"
	}
	mygo.App.WhenReady(func() {
		m.win = mygo.NewWindow(mygo.WindowOptions{Title: tr("Miya Input Tests"), Width: 390, Height: 844, ExtendIntoSafeArea: true, Content: ui.View(m.viewRoot)})
		setAppWindow(m.win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
