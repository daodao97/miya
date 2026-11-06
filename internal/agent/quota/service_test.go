package quota

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fixtureProvider struct {
	id     string
	result Reading
	err    error
}

func (p fixtureProvider) ID() string                            { return p.id }
func (p fixtureProvider) Read(context.Context) (Reading, error) { return p.result, p.err }

type fixtureResetter struct {
	fixtureProvider
	received *ResetAttempt
}

func (p fixtureResetter) Reset(_ context.Context, a ResetAttempt) (ResetResult, error) {
	*p.received = a
	return ResetResult{Outcome: "reset", Reading: Reading{Agent: p.id}}, nil
}

func TestServiceUsesProviderContract(t *testing.T) {
	s := NewService(fixtureProvider{id: "external", result: Reading{Windows: []Window{{Used: 37}}}})
	r := s.Read(context.Background(), "external")
	if r.Agent != "external" || r.At.IsZero() || r.Error != "" || len(r.Windows) != 1 || r.Windows[0].Used != 37 {
		t.Fatalf("provider result lost: %+v", r)
	}
	if r := s.Read(context.Background(), "missing"); r.Error == "" || len(r.Windows) != 0 {
		t.Fatalf("missing provider became real zero quota: %+v", r)
	}
	s = NewService(fixtureProvider{id: "failed", result: Reading{Windows: []Window{{Used: 0}}}, err: errors.New("unavailable")})
	if r := s.Read(context.Background(), "failed"); r.Error != "unavailable" || len(r.Windows) != 0 {
		t.Fatalf("failed read kept a misleading quota: %+v", r)
	}
}

func TestResetRoutesConfirmedOperationAndRejectsAmbiguousLegacyOwner(t *testing.T) {
	var got ResetAttempt
	p := fixtureResetter{fixtureProvider: fixtureProvider{id: "external"}, received: &got}
	a := ResetAttempt{Agent: "external", Key: "same-operation", AccountKey: "same-account", CreditID: "same-card", Home: "same-home"}
	s := NewService(p)
	if _, err := s.Reset(context.Background(), a); err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("reset route changed confirmation: %+v %v", got, err)
	}
	a.Agent = ""
	if _, err := s.Reset(context.Background(), a); err != nil {
		t.Fatal("unambiguous old client rejected:", err)
	}
	s = NewService(p, fixtureResetter{fixtureProvider: fixtureProvider{id: "second"}, received: &got})
	if _, err := s.Reset(context.Background(), a); err == nil {
		t.Fatal("old client reset guessed between multiple owners")
	}
	a.Agent = "unsupported"
	if _, err := s.Reset(context.Background(), a); err == nil {
		t.Fatal("unsupported reset routed to a different provider")
	}
}
