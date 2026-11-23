package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"birdie/internal/bus"
)

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
	printThread(&out, agents, bus.Thread{Title: "Run the tests"}, msgs, false, time.Now())
	text := out.String()
	for _, want := range []string{
		"Run the tests\nThread ",
		"2 messages between Codex and Claude Code",
		"Codex → Claude Code\n",
		"Status: answered; delivered after ",
		"s to Claude Code · api\n",
		"Claude Code · api → Codex",
		"   second line",
		"--full",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	out.Reset()
	printThread(&out, agents, bus.Thread{}, msgs, true, time.Now())
	if strings.Contains(out.String(), "--full") || !strings.Contains(out.String(), strings.Repeat("long ", 199)) {
		t.Errorf("--full should show everything:\n%s", out.String())
	}
}

func TestContinueThread(t *testing.T) {
	ctx := context.Background()
	b, st := statusBus(t)
	b.LoopLimit = 1
	m, err := b.Send(ctx, "codex", bus.SendInput{To: "claude", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{m.ThreadID, m.ID} {
		if _, err := b.Send(ctx, "codex", bus.SendInput{InReplyTo: m.ID, To: "claude", Content: "x"}); err == nil {
			t.Fatal("the limit should stop this")
		}
		var out bytes.Buffer
		if err := continueThread(ctx, &out, st, ref); err != nil || !strings.Contains(out.String(), m.ThreadID) {
			t.Fatalf("continue %s = %q, %v", ref, out.String(), err)
		}
		if _, err := b.Send(ctx, "codex", bus.SendInput{InReplyTo: m.ID, To: "claude", Content: "x"}); err != nil {
			t.Fatalf("after continuing %s: %v", ref, err)
		}
	}
	if err := continueThread(ctx, &bytes.Buffer{}, st, "msg_nope"); err == nil {
		t.Error("continued an unknown message")
	}
}
