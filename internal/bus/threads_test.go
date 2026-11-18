package bus_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"birdie/internal/bus"
)

func TestTitleFrom(t *testing.T) {
	for in, want := range map[string]string{
		"Run the tests":                "Run the tests",
		"\n\n## Deploy plan\nstep one": "Deploy plan",
		"  lots   of\tspace  ":         "lots of space",
		strings.Repeat("word ", 30):    "word word word word word word word word word word word…",
		"":                             "",
	} {
		if got := bus.TitleFrom(in); got != want {
			t.Errorf("TitleFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThreadTitles(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	for _, n := range []string{"alice", "bob", "carol"} {
		addAgent(t, st, n, nil)
	}
	// Named after its first message by default.
	first, err := b.Send(ctx, "alice", bus.SendInput{To: "bob", Content: "Can you review PR #26?\nIt's the web app chat."})
	if err != nil {
		t.Fatal(err)
	}
	if first.ThreadTitle != "Can you review PR #26?" {
		t.Fatalf("default title = %q", first.ThreadTitle)
	}
	// A title given for a thread already going is ignored.
	again, err := b.Send(ctx, "bob", bus.SendInput{InReplyTo: first.ID, Content: "sure", Title: "Something else"})
	if err != nil || again.ThreadTitle != "Can you review PR #26?" {
		t.Fatalf("title after a reply = %q, err %v", again.ThreadTitle, err)
	}
	// One given when starting a thread wins, cleaned to one line.
	named, err := b.Send(ctx, "alice", bus.SendInput{To: "bob", Content: "details inside", Title: "  Release\n v0.6.0  "})
	if err != nil || named.ThreadTitle != "Release v0.6.0" {
		t.Fatalf("given title = %q, err %v", named.ThreadTitle, err)
	}

	// Any member can rename; outsiders can't see or rename it.
	th, err := b.RenameThread(ctx, first.ThreadID, "bob", "Review of PR #26")
	if err != nil || th.Title != "Review of PR #26" || th.RenamedBy != "bob" || th.RenamedAt.IsZero() || th.CreatedBy != "alice" {
		t.Fatalf("rename = %+v, err %v", th, err)
	}
	if _, err := b.RenameThread(ctx, first.ThreadID, "carol", "mine now"); !errors.Is(err, bus.ErrNotFound) {
		t.Fatalf("outsider rename: err = %v, want ErrNotFound", err)
	}
	if _, err := b.ThreadInfo(ctx, first.ThreadID, "carol"); !errors.Is(err, bus.ErrNotFound) {
		t.Fatalf("outsider read: err = %v, want ErrNotFound", err)
	}
	if _, err := b.RenameThread(ctx, first.ThreadID, "alice", " \n "); !errors.Is(err, bus.ErrInvalid) {
		t.Fatalf("empty title: err = %v, want ErrInvalid", err)
	}

	// Messages read afterwards carry the new title.
	got, err := b.ReceiveFor(ctx, bus.ReceiveInput{Agent: "bob"})
	if err != nil || len(got) == 0 {
		t.Fatalf("receive: %v, %v", got, err)
	}
	for _, m := range got {
		want := map[string]string{first.ThreadID: "Review of PR #26", named.ThreadID: "Release v0.6.0"}[m.ThreadID]
		if m.ThreadTitle != want {
			t.Errorf("message %s has title %q, want %q", m.ID, m.ThreadTitle, want)
		}
	}
	msgs, err := b.Thread(ctx, first.ThreadID, "alice")
	if err != nil || msgs[0].ThreadTitle != "Review of PR #26" {
		t.Fatalf("thread read: %v, err %v", msgs, err)
	}
}
