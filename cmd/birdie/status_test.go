package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestWaitingList(t *testing.T) {
	ctx := context.Background()
	b, st, _ := failingBot(t)
	agents, err := b.AgentsWithRecentSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waiting, total, err := st.Waiting(ctx, waitingShown)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printWaiting(&out, agents, waiting, total, time.Now()); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"queued; try 1 failed", "Claude Code · api", "check my calendar", "To follow one: birdie thread MSG_ID"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}
