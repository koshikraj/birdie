package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

// A database from before sessions existed must open and keep its messages.
func TestOpenUpgradesOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bus.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE messages (
	seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, thread_id TEXT NOT NULL,
	in_reply_to TEXT NOT NULL DEFAULT '', sender TEXT NOT NULL, recipient TEXT NOT NULL, content TEXT NOT NULL,
	meta TEXT NOT NULL DEFAULT '{}', status TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '', next_attempt_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL, delivered_at INTEGER);
INSERT INTO messages (id, thread_id, sender, recipient, content, status, created_at)
VALUES ('msg_old', 'thr_old', 'a', 'b', 'hello', 'queued', 1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for range 2 { // opening again must not try to add the columns twice
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		m, err := st.GetMessage(context.Background(), "msg_old")
		if err != nil || m.Content != "hello" || m.ToSession != "" {
			t.Fatalf("m = %+v, %v", m, err)
		}
		st.Close()
	}
}

// Threads from before birdie kept thread records get one when the database
// opens, named after their first message.
func TestOpenNamesOlderThreads(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bus.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for i, m := range []bus.Message{
		{ID: "msg_1", ThreadID: "thr_old", From: "alice", To: "bob", Content: "\n# Old plan\nmore", Status: bus.StatusQueued, CreatedAt: at},
		{ID: "msg_2", ThreadID: "thr_old", From: "bob", To: "alice", Content: "a later message", Status: bus.StatusQueued, CreatedAt: at.Add(time.Second)},
	} {
		if err := st.InsertMessage(ctx, m); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := st.GetThread(ctx, "thr_old"); !errors.Is(err, bus.ErrNotFound) {
		t.Fatalf("before reopening: err = %v, want ErrNotFound", err)
	}
	st.Close()

	if st, err = store.Open(path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	th, err := st.GetThread(ctx, "thr_old")
	if err != nil || th.Title != "Old plan" || th.CreatedBy != "alice" {
		t.Fatalf("after reopening: %+v, err %v", th, err)
	}
	titles, err := st.ThreadTitles(ctx, []string{"thr_old", "thr_old", "thr_missing"})
	if err != nil || len(titles) != 1 || titles["thr_old"] != "Old plan" {
		t.Fatalf("titles = %v, err %v", titles, err)
	}
}

// A send that fails leaves no thread record behind: the title and the first
// message are stored together or not at all.
func TestSendMessageKeepsBothOrNeither(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "bus.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Now().UTC()
	if err := st.InsertMessage(ctx, bus.Message{ID: "msg_taken", ThreadID: "thr_a", From: "alice", To: "bob", Content: "x", Status: bus.StatusQueued, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	// The message id is taken, so the insert fails, and the thread must not be recorded.
	err = st.SendMessage(ctx, bus.Message{ID: "msg_taken", ThreadID: "thr_new", From: "alice", To: "bob", Content: "x", Status: bus.StatusQueued, CreatedAt: at},
		bus.Thread{ID: "thr_new", Title: "Alice private title", CreatedBy: "alice", CreatedAt: at})
	if err == nil {
		t.Fatal("a duplicate message id was accepted")
	}
	if _, err := st.GetThread(ctx, "thr_new"); !errors.Is(err, bus.ErrNotFound) {
		t.Fatalf("failed send left a thread record: err = %v", err)
	}
	// A cancelled send keeps nothing either.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := st.SendMessage(cancelled, bus.Message{ID: "msg_c", ThreadID: "thr_c", From: "alice", To: "bob", Content: "x", Status: bus.StatusQueued, CreatedAt: at},
		bus.Thread{ID: "thr_c", Title: "never", CreatedBy: "alice", CreatedAt: at}); err == nil {
		t.Fatal("a cancelled send succeeded")
	}
	if _, err := st.GetThread(ctx, "thr_c"); !errors.Is(err, bus.ErrNotFound) {
		t.Fatalf("cancelled send left a thread record: err = %v", err)
	}
	// The next real first message names it.
	if err := st.SendMessage(ctx, bus.Message{ID: "msg_carol", ThreadID: "thr_new", From: "carol", To: "bob", Content: "x", Status: bus.StatusQueued, CreatedAt: at},
		bus.Thread{ID: "thr_new", Title: "Carol title", CreatedBy: "carol", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if th, err := st.GetThread(ctx, "thr_new"); err != nil || th.Title != "Carol title" || th.CreatedBy != "carol" {
		t.Fatalf("thread = %+v, err %v", th, err)
	}
}
