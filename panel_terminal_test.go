package main

import (
	"net"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
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
	p := app.panel()
	s := &panelSurface{id: 1, kind: surfaceTerminal, starting: true}
	p.visible, p.active, p.surfaces = true, 0, []*panelSurface{s}
	tt.Frame()
	if !tt.HasText("正在启动终端…") {
		t.Fatal("terminal must first show its loading state")
	}
	s.starting, s.term = false, term
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
