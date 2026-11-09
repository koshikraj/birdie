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
	printThread(&out, agents, msgs, false, time.Now())
	text := out.String()
	for _, want := range []string{
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
	printThread(&out, agents, msgs, true, time.Now())
	if strings.Contains(out.String(), "--full") || !strings.Contains(out.String(), strings.Repeat("long ", 199)) {
		t.Errorf("--full should show everything:\n%s", out.String())
	}
}
