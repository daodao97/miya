package widgets

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/contacts"
)

func TestContactUIRetriesAcceptedSaveWithoutCreatingAnotherContact(t *testing.T) {
	s := contacts.New(t.TempDir())
	p := ContactUI{}
	v := ContactView{Source: "one", Snapshot: s.Snapshot(), Connected: true}
	var ids []string
	v.Call = func(r contacts.Request, done func(appmodel.ContactResult, error)) {
		ids = append(ids, r.ID)
		state, _, err := s.Apply(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 1 {
			done(appmodel.ContactResult{}, errors.New("response was lost"))
			return
		}
		done(appmodel.ContactResult{State: state, ProfileID: s.ProfileForRequest(r.ID)}, nil)
	}
	r := contacts.Request{Op: "save", Profile: contacts.Profile{Name: "Friend", Agent: "codex"}}
	p.Submit(r, v)
	if p.Err == "" || p.Busy {
		t.Fatal("failed request did not return to an editable state")
	}
	p.Submit(r, v)
	if len(ids) != 2 || ids[0] != ids[1] || len(s.Snapshot().Profiles) != 1 || p.Detail == "" {
		t.Fatal("retry duplicated contact or lost selection", ids)
	}
}

func TestContactUIRejectsOldBackendAndSeparatesSources(t *testing.T) {
	p := ContactUI{}
	p.Use(contacts.Snapshot{Version: 1, Revision: 99, Profiles: []contacts.Profile{{ID: "old", Name: "Old"}}})
	called := false
	v := ContactView{Connected: true, Call: func(contacts.Request, func(appmodel.ContactResult, error)) { called = true }}
	p.Submit(contacts.Request{Op: "save"}, v)
	if called || p.Err == "" {
		t.Fatal("sent contact operation to an old backend")
	}
	v.Source = "other"
	v.Snapshot = contacts.Snapshot{Version: 1, Revision: 1, Profiles: []contacts.Profile{}}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c, v) }, 390, 844)
	tt.Frame()
	if len(p.State.Profiles) != 0 || p.State.Revision != 1 || !tt.HasText("No contacts here yet") {
		t.Fatal("contacts leaked between desktops")
	}
}
