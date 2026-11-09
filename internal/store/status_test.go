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
