package main

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"birdie/internal/bus"
)

// sessionAddress is how other agents reach a session.
func sessionAddress(s bus.Session) string {
	if s.Name != "" {
		return s.Agent + "/" + s.Name
	}
	return s.Agent + "/" + s.ID
}

// sessionState says what a session is doing: busy, idle 12m or waiting 3m
// when its agent says, live when it doesn't, and offline 20m once it stopped.
func sessionState(s bus.Session, now time.Time) string {
	if !s.Live(now) {
		return "offline " + span(now.Sub(s.StoppedAt()))
	}
	switch s.State {
	case bus.StateBusy:
		return "busy"
	case bus.StateIdle, bus.StateWaiting:
		return s.State + " " + span(now.Sub(s.ActiveAt))
	}
	return "live"
}

// span says roughly how long d is: 40s, 12m, 3h, 2d.
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d/time.Second), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// homePath shortens a path in the home folder to start with ~.
func homePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(os.PathSeparator)); ok {
		return "~" + string(os.PathSeparator) + rest
	}
	return p
}

// printable replaces control characters in text from other agents, so it
// can't move the cursor or recolor the terminal. With lines set, newlines
// stay; otherwise they and tabs become spaces, to keep tables in line.
func printable(s string, lines bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && lines:
			return r
		case r == '\n' || r == '\t' || r == '\r':
			return ' '
		case unicode.IsControl(r):
			return '�'
		}
		return r
	}, s)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
