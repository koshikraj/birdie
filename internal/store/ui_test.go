package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

func pairWant(a, b string, n int) store.Pair { return store.Pair{A: a, B: b, Count: n} }

func TestWebAppQueries(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	addAgent(t, st, "alice", bus.DeliveryPull)
	addAgent(t, st, "bob", bus.DeliveryPull)
	addAgent(t, st, "carol", bus.DeliveryPull)
	now := time.Now()
	insert(t, st, "m1", "t1", "alice", "bob", now.Add(-3*time.Minute))
	insert(t, st, "m2", "t1", "bob", "alice", now.Add(-2*time.Minute))
	insert(t, st, "m3", "t2", "carol", "alice", now.Add(-time.Minute))
	insert(t, st, "m4", "t2", bus.SystemSender, "carol", now)
	insert(t, st, "old", "t0", "alice", "bob", now.Add(-48*time.Hour))
	if err := st.RecordFailure(ctx, "m3", "HTTP 502", now.Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}

	recent, err := st.RecentMessages(ctx, 2)
	if err != nil || len(recent) != 2 || recent[0].ID != "old" || recent[1].ID != "m4" {
		t.Fatalf("recent = %+v, %v", recent, err)
	}

	threads, err := st.RecentThreads(ctx, 10)
	if err != nil || len(threads) != 3 {
		t.Fatalf("threads = %+v, %v", threads, err)
	}
	byID := map[string]int{}
	for i, th := range threads {
		byID[th.ID] = i
	}
	t2 := threads[byID["t2"]]
	if t2.Count != 2 || !t2.Problem || t2.Last.ID != "m4" || len(t2.Participants) != 3 {
		t.Fatalf("t2 = %+v", t2)
	}
	if t1 := threads[byID["t1"]]; t1.Problem || t1.Last.ID != "m2" || len(t1.Participants) != 2 {
		t.Fatalf("t1 = %+v", t1)
	}

	pairs, err := st.PairsSince(ctx, now.Add(-time.Hour))
	if err != nil || len(pairs) != 2 || pairs[0] != (pairWant("alice", "bob", 2)) || pairs[1] != (pairWant("alice", "carol", 1)) {
		t.Fatalf("pairs = %+v, %v", pairs, err)
	}
	n, last, err := st.SentSince(ctx, now.Add(-time.Hour))
	if err != nil || n != 3 || last.UnixMilli() != now.Add(-time.Minute).UnixMilli() {
		t.Fatalf("sent = %d, %v, %v", n, last, err)
	}
}

func TestStateCanBeTakenOnce(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if err := st.SetState(ctx, "ui_login:abc", "123"); err != nil {
		t.Fatal(err)
	}
	if v, err := st.TakeState(ctx, "ui_login:abc"); err != nil || v != "123" {
		t.Fatalf("take = %q, %v", v, err)
	}
	if _, err := st.TakeState(ctx, "ui_login:abc"); !errors.Is(err, bus.ErrNotFound) {
		t.Fatalf("second take: %v", err)
	}
	st.SetState(ctx, "ui_session:old", "100")
	st.SetState(ctx, "ui_session:new", "9999999999")
	st.SetState(ctx, "other", "1")
	if err := st.DeleteExpiredState(ctx, "ui_session:", time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetState(ctx, "ui_session:old"); !errors.Is(err, bus.ErrNotFound) {
		t.Fatal("an expired session was kept")
	}
	for _, k := range []string{"ui_session:new", "other"} {
		if _, err := st.GetState(ctx, k); err != nil {
			t.Fatalf("%s removed: %v", k, err)
		}
	}
}
