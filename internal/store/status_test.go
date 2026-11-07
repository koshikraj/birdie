package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

func open(t *testing.T) *store.SQLite {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addAgent(t *testing.T, st *store.SQLite, name, delivery string) {
	t.Helper()
	a := bus.Agent{Name: name, TokenHash: name + "-hash", Delivery: delivery, CreatedAt: time.Now()}
	if err := st.CreateAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func insert(t *testing.T, st *store.SQLite, id, thread, from, to string, at time.Time) {
	t.Helper()
	m := bus.Message{ID: id, ThreadID: thread, From: from, To: to, Content: "hi " + id, Status: bus.StatusQueued, CreatedAt: at}
	if err := st.InsertMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func TestClosedSessionsAreRememberedButNotLive(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	s := bus.Session{ID: "s1", Agent: "claude", Name: "api", Branch: "main", Tier: bus.TierPush, State: bus.StateBusy, ActiveAt: now, LastSeen: now}
	if err := st.UpsertSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	live, _ := st.LiveSessions(ctx, "claude", now.Add(-time.Minute))
	if len(live) != 1 || live[0].State != bus.StateBusy || live[0].Branch != "main" || !live[0].EndedAt.IsZero() {
		t.Fatalf("live = %+v", live)
	}

	ended := now.Add(time.Second)
	if err := st.EndSession(ctx, "claude", "s1", ended); err != nil {
		t.Fatal(err)
	}
	if live, _ := st.LiveSessions(ctx, "claude", now.Add(-time.Minute)); len(live) != 0 {
		t.Fatalf("a closed session still counts as running: %+v", live)
	}
	recent, _ := st.RecentSessions(ctx, "claude", now.Add(-time.Minute))
	if len(recent) != 1 || !recent[0].EndedAt.Equal(ended) || recent[0].Live(ended) {
		t.Fatalf("recent = %+v", recent)
	}

	// Reporting again means it is running again.
	s.LastSeen = ended.Add(time.Second)
	if err := st.UpsertSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if live, _ := st.LiveSessions(ctx, "claude", now.Add(-time.Minute)); len(live) != 1 {
		t.Fatalf("a session that reported again isn't running: %+v", live)
	}
}

// A database from before session states existed keeps its sessions.
func TestOpenUpgradesOldSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bus.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Now().UnixMilli()
	if _, err := db.Exec(`
CREATE TABLE sessions (agent TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '',
	tier TEXT NOT NULL DEFAULT 'pull', active_at INTEGER NOT NULL, last_seen INTEGER NOT NULL, PRIMARY KEY (agent, id));
INSERT INTO sessions (agent, id, name, tier, active_at, last_seen) VALUES ('claude', 's1', 'api', 'push', ?, ?);`, seen, seen); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	live, err := st.LiveSessions(context.Background(), "claude", time.Now().Add(-time.Minute))
	if err != nil || len(live) != 1 || live[0].Name != "api" || live[0].State != "" {
		t.Fatalf("live = %+v, %v", live, err)
	}
}

func TestDeliveries(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	addAgent(t, st, "alice", bus.DeliveryPull)
	addAgent(t, st, "hook", "webhook")
	addAgent(t, st, "gone", "webhook")
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)

	// hook got one message, then the next two failed: one is retrying, one was
	// given up on.
	insert(t, st, "m1", "t1", "alice", "hook", t0)
	if err := st.MarkDelivered(ctx, "m1", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	insert(t, st, "m2", "t2", "alice", "hook", t0.Add(time.Minute))
	retry := time.Now().Add(time.Minute)
	if err := st.RecordFailure(ctx, "m2", "HTTP 502", retry, false); err != nil {
		t.Fatal(err)
	}
	insert(t, st, "m3", "t3", "alice", "gone", t0)
	if err := st.RecordFailure(ctx, "m3", "no such host", time.Now(), true); err != nil {
		t.Fatal(err)
	}

	d, err := st.Deliveries(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	h := d["hook"]
	if h.Waiting != 1 || !h.Oldest.Equal(t0.Add(time.Minute)) || !h.Delivered.Equal(t0.Add(time.Second)) {
		t.Fatalf("hook counts = %+v", h)
	}
	if h.Error != "HTTP 502" || h.ErrorID != "m2" || h.Retry.UnixMilli() != retry.UnixMilli() || h.GaveUp != 0 {
		t.Fatalf("hook error = %+v", h)
	}
	g := d["gone"]
	if g.Waiting != 0 || g.GaveUp != 1 || g.Error != "no such host" || !g.Retry.IsZero() {
		t.Fatalf("gone = %+v", g)
	}

	// Once a message gets through, the earlier failure is history.
	insert(t, st, "m4", "t4", "alice", "hook", time.Now())
	if err := st.MarkDelivered(ctx, "m4", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	d, _ = st.Deliveries(ctx, time.Now().Add(-time.Hour))
	if h := d["hook"]; h.Error != "" || h.Waiting != 1 {
		t.Fatalf("after a delivery, hook = %+v", h)
	}
	// And failures before the time asked about aren't counted.
	d, _ = st.Deliveries(ctx, time.Now().Add(time.Hour))
	if g := d["gone"]; g.GaveUp != 0 {
		t.Fatalf("old failures counted: %+v", g)
	}
}

func TestTrackThreadAndWaiting(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	addAgent(t, st, "alice", bus.DeliveryPull)
	addAgent(t, st, "bob", bus.DeliveryPull)
	now := time.Now()
	insert(t, st, "m1", "t1", "alice", "bob", now.Add(-time.Minute))
	insert(t, st, "m2", "t1", "bob", "alice", now)
	insert(t, st, "m3", "t2", "alice", "bob", now)

	got, err := st.ClaimInbox(ctx, bus.ClaimFilter{Agent: "bob", Thread: "t1", Session: "s9", Unpinned: true, LiveSince: now.Add(-time.Minute), Limit: 10}, now)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim = %+v, %v", got, err)
	}

	msgs, err := st.TrackThread(ctx, "t1")
	if err != nil || len(msgs) != 2 {
		t.Fatalf("thread = %+v, %v", msgs, err)
	}
	if m := msgs[0]; m.ID != "m1" || m.Status != bus.StatusDelivered || m.TakenBy != "s9" || m.Delivered.IsZero() {
		t.Fatalf("taken message = %+v", m)
	}
	if m := msgs[1]; m.From != "bob" || m.Status != bus.StatusQueued || m.TakenBy != "" || !m.Delivered.IsZero() {
		t.Fatalf("waiting message = %+v", m)
	}

	waiting, total, err := st.Waiting(ctx, 1)
	if err != nil || total != 2 || len(waiting) != 1 || waiting[0].ID != "m2" {
		t.Fatalf("waiting = %+v, %d, %v", waiting, total, err)
	}
}
