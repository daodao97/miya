package main

import (
	desktopui "miya/internal/ui"
	net "net"
	testing "testing"

	terminal "github.com/egoist/mygo/plugins/terminal"
)

func TestPanelTerminalFocusAfterStartup(t *testing.T) {
	conn, peer := net.Pipe()
	t.Cleanup(func() { peer.Close() })
	term, err := terminal.New(terminal.Options{Conn: conn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { term.Close() })
	app, tt := testApp(t)
	p := app.Panel()
	s := &desktopui.PanelSurface{Id: 1, Kind: desktopui.SurfaceTerminal, Starting: true}
	p.Visible, p.Active, p.Surfaces = true, 0, []*desktopui.PanelSurface{s}
	tt.Frame()
	if !tt.HasText("Starting terminal…") {
		t.Fatal("terminal must first show its loading state")
	}
	s.Starting, s.Term = false, term
	tt.Frame()
	if !tt.Focused("Terminal") {
		t.Fatal("terminal did not take focus after startup")
	}
	if err := tt.Click("Message Codex"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.Focused("Message Codex") {
		t.Fatal("terminal stole focus back from the composer")
	}
}
