package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"miya/internal/webservice"

	"github.com/egoist/mygo/ui"
)

func TestMobileWebServiceNavigation(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, thread := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/thread=%v", width, thread), func(t *testing.T) {
				m := testMobileApp()
				m.state.WebPorts = true
				if thread {
					m.openThread(m.state.Projects[0].Threads[0])
				}
				m.draft = "保留草稿"
				originalPath, originalThread := m.router().Path(), m.open
				tt := ui.NewTester(m.viewRoot, width, 844)
				tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
				tt.SetPreferences(ui.Preferences{TextScale: 1.8})
				tt.Frame()
				if err := tt.Click("More"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if err := tt.Click("Web services"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if m.router().Path() != "/web" || m.conversationMenu || m.open != originalThread || m.draft != "保留草稿" {
					t.Fatal("web navigation changed the conversation")
				}
				input, ok := tt.Find("Web port")
				button, buttonOK := tt.Find("Open web service")
				if !ok || !buttonOK || input.H < 44 || button.H < 44 || button.Y < input.Y+input.H || button.Y+button.H > 810 {
					t.Fatalf("web controls outside safe area: input=%v button=%v", input, button)
				}
				if err := tt.Click("Web port"); err != nil {
					t.Fatal(err)
				}
				tt.Type("5173")
				if m.webPort != "5173" {
					t.Fatal("port input did not update")
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("web-service-%d-thread-%v", width, thread))
				if err := tt.Click("Back"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if m.router().Path() != originalPath || m.open != originalThread || m.draft != "保留草稿" {
					t.Fatal("return from web page lost conversation or draft")
				}
			})
		}
	}
}

func TestMobileWebServiceOldDesktopAndInvalidPort(t *testing.T) {
	m := testMobileApp()
	m.router().Push("/web")
	m.webPort = "65536"
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	if !tt.HasText("Update Miya on your desktop first") || !tt.HasText("1 and 65535") {
		t.Fatal("missing compatibility or validation hint")
	}
	m.connected = false
	tt.Frame()
	if !tt.HasText("Connect to your desktop") {
		t.Fatal("missing disconnected hint")
	}
}

func TestMobileWebServicesListKeepsConversationProject(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			m.state.WebPorts, m.state.WebServices, m.state.Runtime = true, true, true
			m.selected = "/a"
			m.openThread(m.state.Projects[1].Threads[0])
			m.draft = "保留草稿"
			tt := ui.NewTester(m.viewRoot, width, 844)
			tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
			tt.SetPreferences(ui.Preferences{TextScale: 1.8})
			tt.Frame()
			if err := tt.Click("More"); err != nil {
				t.Fatal(err)
			}
			tt.Advance(time.Second)
			if err := tt.Click("Web services"); err != nil {
				t.Fatal(err)
			}
			tt.Advance(time.Second)
			if m.webProject != "/b" || m.webServices.project != "/b" {
				t.Fatal("service list used selected project instead of conversation project")
			}
			m.webServices.services = []webservice.Service{{Port: 3000, Process: "node"}, {Port: 5173, Process: "vite"}}
			tt.Frame()
			for _, label := range []string{"proj-b", "Port 3000", "Port 5173", "Refresh web services", "Enter port manually"} {
				if !tt.HasText(label) {
					t.Fatalf("missing list control %s", label)
				}
			}
			if _, ok := tt.Find("Web port"); ok {
				t.Fatal("manual input should be collapsed by default")
			}
			for _, label := range []string{"Open http://localhost:3000", "Open http://localhost:5173"} {
				row, ok := tt.Find(label)
				if !ok || row.H < 44 || row.Y < 59 || row.Y+row.H > 810 {
					t.Fatalf("service outside safe area: %v", row)
				}
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("web-services-list-%d", width))
			if err := tt.Click("Enter port manually"); err != nil {
				t.Fatal(err)
			}
			if !m.webManual {
				t.Fatal("manual entry did not expand")
			}
			if err := tt.Click("Back"); err != nil {
				t.Fatal(err)
			}
			tt.Advance(time.Second)
			if m.open != 2 || m.draft != "保留草稿" || m.router().Path() != "/thread" {
				t.Fatal("web list lost conversation state")
			}
		})
	}
}

func TestMobileWebServicesEmptyLoadingRefreshAndCancel(t *testing.T) {
	m := testMobileApp()
	m.state.WebPorts, m.state.WebServices, m.state.Runtime = true, true, true
	m.openWebServices("/a")
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	if !tt.HasText("No project HTTP services found") {
		t.Fatal("missing empty state")
	}
	state := m.webServices
	state.loading = true
	tt.Frame()
	if !tt.HasText("Finding project web services") {
		t.Fatal("missing loading state")
	}
	state.loading = false
	state.err = "Failed to load. Refresh."
	tt.Frame()
	if !tt.HasText("Failed to load") {
		t.Fatal("missing error state")
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	if err := tt.Click("Refresh web services"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ctx.Err() == nil || m.webServices == state || m.webServices.err != "" {
		t.Fatal("refresh did not discard previous request")
	}
	ctx, cancel = context.WithCancel(context.Background())
	m.webServices.cancel = cancel
	m.openWebServices("/b")
	if ctx.Err() == nil || m.webProject != "/b" {
		t.Fatal("changing project did not cancel discovery")
	}
	m.projectWebServices()
	ctx, cancel = context.WithCancel(context.Background())
	m.webServices.cancel = cancel
	m.disconnectDesktop(false)
	if ctx.Err() == nil || m.webServices != nil {
		t.Fatal("disconnect retained project discovery")
	}
}
