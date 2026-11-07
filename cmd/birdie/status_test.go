package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:                  "0s",
		40 * time.Second:              "40s",
		12 * time.Minute:              "12m",
		3*time.Hour + 59*60e9:         "3h",
		47 * time.Hour:                "47h",
		50 * time.Hour:                "2d",
		10 * 24 * time.Hour:           "10d",
		time.Minute - time.Nanosecond: "59s",
	} {
		if got := span(d); got != want {
			t.Errorf("span(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSessionState(t *testing.T) {
	now := time.Now()
	live := bus.Session{LastSeen: now, ActiveAt: now.Add(-12 * time.Minute)}
	for want, s := range map[string]bus.Session{
		"busy":        withState(live, bus.StateBusy),
		"idle 12m":    withState(live, bus.StateIdle),
		"waiting 12m": withState(live, bus.StateWaiting),
		"live":        live,
		"offline 20m": {LastSeen: now.Add(-20 * time.Minute), State: bus.StateBusy},
		"offline 3m":  {LastSeen: now, EndedAt: now.Add(-3 * time.Minute)},
	} {
		if got := sessionState(s, now); got != want {
			t.Errorf("sessionState(%+v) = %q, want %q", s, got, want)
		}
	}
}

func withState(s bus.Session, state string) bus.Session {
	s.State = state
	return s
}

func TestPrintable(t *testing.T) {
	in := "red \x1b[31mtext\x1b[0m\ttab\r\nnext"
	if got := printable(in, false); strings.ContainsAny(got, "\x1b\t\r\n") {
		t.Fatalf("printable(lines=false) = %q", got)
	}
	if got := printable(in, true); strings.Contains(got, "\x1b") || !strings.Contains(got, "\nnext") {
		t.Fatalf("printable(lines=true) = %q", got)
	}
}

// statusBus is a bus with a Claude session, a webhook bot that is failing, and
// a pull agent with mail but no session.
func statusBus(t *testing.T) (*bus.Bus, *store.SQLite) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "bus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	b := bus.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.LoopLimit = -1
	for _, a := range []bus.Agent{
		{Name: "claude", Delivery: bus.DeliveryPull, Description: "Claude Code"},
		{Name: "codex", Delivery: bus.DeliveryPull},
		{Name: "bot", Delivery: "webhook", Config: map[string]string{"url": "http://x"}},
	} {
		a.TokenHash, a.CreatedAt = a.Name, time.Now()
		if err := st.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.ReportSession(ctx, "claude", "s-api", bus.SessionInput{Name: "api", Tier: bus.TierPush, State: bus.StateBusy, Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := b.ReportSession(ctx, "claude", "s-old", bus.SessionInput{Name: "old", Tier: bus.TierPush}); err != nil {
		t.Fatal(err)
	}
	if err := b.EndSession(ctx, "claude", "s-old"); err != nil {
		t.Fatal(err)
	}
	return b, st
}

func TestStatusTables(t *testing.T) {
	ctx := context.Background()
	b, st := statusBus(t)
	m, err := b.Send(ctx, "claude", bus.SendInput{To: "bot", Content: "check my calendar", FromSession: "s-api"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordFailure(ctx, m.ID, "HTTP 502: Bad Gateway.", time.Now().Add(30*time.Second), false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(ctx, "claude", bus.SendInput{To: "codex", Content: "review this"}); err != nil {
		t.Fatal(err)
	}

	agents, err := b.AgentsWithRecentSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := st.Deliveries(ctx, time.Now().Add(-problemWindow))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var out bytes.Buffer
	if err := printSessions(&out, agents, false, now); err != nil {
		t.Fatal(err)
	}
	if err := printAgents(&out, agents, deliveries, true, now); err != nil {
		t.Fatal(err)
	}
	waiting, total, err := st.Waiting(ctx, waitingShown)
	if err != nil {
		t.Fatal(err)
	}
	if err := printWaiting(&out, agents, waiting, total, now); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"claude/api", "busy", "main",
		"1 more session stopped in the last day",
		"bot: the last delivery failed ",
		"s ago: HTTP 502: Bad Gateway. Next try in ",
		"s. Details: birdie thread " + m.ID + "\n",
		"codex: no session is running, so 1 message will wait until one starts.",
		"queued; try 1 failed",
		"claude/api", // who sent the waiting message
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "claude/old") {
		t.Errorf("a stopped session was listed without --all:\n%s", text)
	}

	out.Reset()
	printSessions(&out, agents, true, now)
	if !strings.Contains(out.String(), "claude/old") || !strings.Contains(out.String(), "offline ") {
		t.Errorf("--all should list the stopped session:\n%s", out.String())
	}
}

func TestPrintThread(t *testing.T) {
	ctx := context.Background()
	b, st := statusBus(t)
	q, err := b.Send(ctx, "codex", bus.SendInput{To: "claude", Content: "first line\nsecond line\n" + strings.Repeat("long ", 200)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b.ReceiveFor(ctx, bus.ReceiveInput{Agent: "claude", Session: "s-api"}); err != nil || len(got) != 1 {
		t.Fatalf("take = %+v, %v", got, err)
	}
	if _, err := b.Send(ctx, "claude", bus.SendInput{InReplyTo: q.ID, Content: "done", FromSession: "s-api"}); err != nil {
		t.Fatal(err)
	}
	msgs, err := st.TrackThread(ctx, q.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	agents, _ := b.AgentsWithRecentSessions(ctx)

	var out bytes.Buffer
	printThread(&out, agents, msgs, false, time.Now())
	text := out.String()
	for _, want := range []string{
		"2 messages between codex and claude",
		"codex → claude\n",
		"Status: answered; delivered after ",
		"s to claude/api\n",
		"claude/api → codex",
		"   second line",
		"--full",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	out.Reset()
	printThread(&out, agents, msgs, true, time.Now())
	if strings.Contains(out.String(), "--full") || !strings.Contains(out.String(), strings.Repeat("long ", 199)) {
		t.Errorf("--full should show everything:\n%s", out.String())
	}
}
