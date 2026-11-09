package main

import (
	"strings"
	"testing"
	"time"

	"birdie/internal/bus"
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
