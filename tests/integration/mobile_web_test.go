package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	webservice "miya/internal/runtime/web"
)

func TestMobileWebServiceNavigation(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, thread := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/thread=%v", width, thread), func(t *testing.T) {
				m := testMobileApp()
				m.State.WebPorts = true
				if thread {
					m.OpenThread(m.State.Projects[0].Threads[0])
				}
				m.Draft = "保留草稿"
				originalPath, originalThread := m.Router().Path(), m.Open
				tt := ui.NewTester(m.ViewRoot, width, 844)
				setSafeInsets(tt, safeInsets{Top: 59, Bottom: 34})
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
				if m.Router().Path() != "/web" || m.ConversationMenu || m.Open != originalThread || m.Draft != "保留草稿" {
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
				if m.WebPort != "5173" {
					t.Fatal("port input did not update")
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("web-service-%d-thread-%v", width, thread))
				if err := tt.Click("Back"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
				if m.Router().Path() != originalPath || m.Open != originalThread || m.Draft != "保留草稿" {
					t.Fatal("return from web page lost conversation or draft")
				}
			})
		}
	}
}

func TestMobileWebServiceOldDesktopAndInvalidPort(t *testing.T) {
	m := testMobileApp()
	m.Router().Push("/web")
	m.WebPort = "65536"
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	if !tt.HasText("Update Miya on your desktop first") || !tt.HasText("1 and 65535") {
		t.Fatal("missing compatibility or validation hint")
	}
	m.Connected = false
	tt.Frame()
	if !tt.HasText("Connect to your desktop") {
		t.Fatal("missing disconnected hint")
	}
}

func TestMobileWebServicesListKeepsConversationProject(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			m.State.WebPorts, m.State.WebServices, m.State.Runtime = true, true, true
			m.Selected = "/a"
			m.OpenThread(m.State.Projects[1].Threads[0])
			m.Draft = "保留草稿"
			tt := ui.NewTester(m.ViewRoot, width, 844)
			setSafeInsets(tt, safeInsets{Top: 59, Bottom: 34})
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
			if m.WebProject != "/b" || m.WebServices.Project != "/b" {
				t.Fatal("service list used selected project instead of conversation project")
			}
			m.WebServices.Services = []webservice.Service{{Port: 3000, Process: "node"}, {Port: 5173, Process: "vite"}}
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
			if !m.WebManual {
				t.Fatal("manual entry did not expand")
			}
			if err := tt.Click("Back"); err != nil {
				t.Fatal(err)
			}
			tt.Advance(time.Second)
			if m.Open != 2 || m.Draft != "保留草稿" || m.Router().Path() != "/thread" {
				t.Fatal("web list lost conversation state")
			}
		})
	}
}

func TestMobileWebServicesEmptyLoadingRefreshAndCancel(t *testing.T) {
	m := testMobileApp()
	m.State.WebPorts, m.State.WebServices, m.State.Runtime = true, true, true
	m.OpenWebServices("/a")
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	if !tt.HasText("No project HTTP services found") {
		t.Fatal("missing empty state")
	}
	state := m.WebServices
	state.Loading = true
	tt.Frame()
	if !tt.HasText("Finding project web services") {
		t.Fatal("missing loading state")
	}
	state.Loading = false
	state.Err = "Failed to load. Refresh."
	tt.Frame()
	if !tt.HasText("Failed to load") {
		t.Fatal("missing error state")
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.Cancel = cancel
	if err := tt.Click("Refresh web services"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ctx.Err() == nil || m.WebServices == state || m.WebServices.Err != "" {
		t.Fatal("refresh did not discard previous request")
	}
	ctx, cancel = context.WithCancel(context.Background())
	m.WebServices.Cancel = cancel
	m.OpenWebServices("/b")
	if ctx.Err() == nil || m.WebProject != "/b" {
		t.Fatal("changing project did not cancel discovery")
	}
	m.ProjectWebServices()
	ctx, cancel = context.WithCancel(context.Background())
	m.WebServices.Cancel = cancel
	m.DisconnectDesktop(false)
	if ctx.Err() == nil || m.WebServices != nil {
		t.Fatal("disconnect retained project discovery")
	}
}
