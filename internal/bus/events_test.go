package bus_test

import (
	"context"
	"testing"
	"time"

	"birdie/internal/bus"
)

func next(t *testing.T, ch <-chan bus.Event) bus.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return bus.Event{}
}

func TestWatchSeesMessagesMove(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	addAgent(t, st, "alice", nil)
	addAgent(t, st, "bob", nil)
	events, stop := b.Watch()
	defer stop()

	if err := b.ReportSession(ctx, "bob", "s1", bus.SessionInput{Name: "api", Tier: bus.TierPush, State: bus.StateBusy}); err != nil {
		t.Fatal(err)
	}
	if e := next(t, events); e.Kind != "session" || e.Type != "reported" || e.Agent != "bob" || e.Name != "api" || e.State != bus.StateBusy {
		t.Fatalf("session event = %+v", e)
	}

	q, err := b.Send(ctx, "alice", bus.SendInput{To: "bob", Content: "hello there"})
	if err != nil {
		t.Fatal(err)
	}
	if e := next(t, events); e.Kind != "message" || e.Type != bus.StatusQueued || e.ID != q.ID || e.From != "alice" || e.To != "bob" || e.Text != "hello there" {
		t.Fatalf("queued event = %+v", e)
	}

	if _, err := b.ReceiveFor(ctx, bus.ReceiveInput{Agent: "bob", Session: "s1", Lease: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if e := next(t, events); e.Type != bus.StatusClaimed || e.ID != q.ID || e.TakenBy != "s1" {
		t.Fatalf("claimed event = %+v", e)
	}
	if err := b.Ack(ctx, "bob", q.ID); err != nil {
		t.Fatal(err)
	}
	if e := next(t, events); e.Type != bus.StatusDelivered || e.ID != q.ID {
		t.Fatalf("delivered event = %+v", e)
	}

	a, err := b.Send(ctx, "bob", bus.SendInput{InReplyTo: q.ID, Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if e := next(t, events); e.Type != bus.StatusQueued || e.ID != a.ID || e.InReplyTo != q.ID {
		t.Fatalf("answer event = %+v", e)
	}
	if e := next(t, events); e.Type != bus.StatusReplied || e.ID != q.ID {
		t.Fatalf("replied event = %+v", e)
	}

	stop()
	if _, err := b.Send(ctx, "alice", bus.SendInput{To: "bob", Content: "after"}); err != nil {
		t.Fatal(err)
	}
	select {
	case e, ok := <-events:
		if ok {
			t.Fatalf("got %+v after stopping", e)
		}
	case <-time.After(50 * time.Millisecond):
	}
}
