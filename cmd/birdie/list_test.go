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

func TestSessionList(t *testing.T) {
	ctx := context.Background()
	b, _ := statusBus(t)
	agents, err := b.AgentsWithRecentSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var out bytes.Buffer
	if err := printSessions(&out, agents, false, now); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"claude/api", "busy", "main", "1 more session stopped in the last day"} {
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

func failingBot(t *testing.T) (*bus.Bus, *store.SQLite, bus.Message) {
	t.Helper()
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

	return b, st, m
}

func TestAgentList(t *testing.T) {
	ctx := context.Background()
	b, st, m := failingBot(t)
	agents, err := b.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := st.Deliveries(ctx, time.Now().Add(-problemWindow))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printAgents(&out, agents, deliveries, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"bot: the last delivery failed ",
		"s ago: HTTP 502: Bad Gateway. Next try in ",
		"s. Details: birdie thread " + m.ID + "\n",
		"Codex: no session is running, so 1 message will wait until one starts.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}
