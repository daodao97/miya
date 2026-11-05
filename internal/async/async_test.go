package async

import (
	"context"
	"testing"
	"time"
)

func TestExecutorRunsInOrderAndDoWaits(t *testing.T) {
	e := NewExecutor()
	quit := make(chan struct{})
	defer close(quit)
	go e.Loop(quit)
	var got []int
	for i := range 5 {
		e.Push(func() { got = append(got, i) })
	}
	e.Do(func() { got = append(got, 5) })
	for i, v := range got {
		if v != i {
			t.Fatalf("ran out of order: %v", got)
		}
	}
	if len(got) != 6 {
		t.Fatalf("Do returned before earlier work ran: %v", got)
	}
}

func TestOnMainReturnsWhenTheContextEnds(t *testing.T) {
	v, ok := OnMain(context.Background(), func(f func()) { go f() }, func() int { return 7 })
	if !ok || v != 7 {
		t.Fatal(v, ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	never := func(func()) {}
	if v, ok := OnMain(ctx, never, func() int { return 7 }); ok || v != 0 {
		t.Fatal("returned a result the main thread never ran", v, ok)
	}
}
