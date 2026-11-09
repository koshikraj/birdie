package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

// threadCmd shows every message in a thread and how far each one got.
func threadCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("thread", flag.ContinueOnError)
	db := fs.String("db", defaultDB(), "SQLite database path")
	full := fs.Bool("full", false, "show each message in full")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: birdie thread THREAD_ID|MSG_ID [--full]")
	}
	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	msgs, err := st.TrackThread(ctx, pos[0])
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		m, err := st.GetMessage(ctx, pos[0])
		if errors.Is(err, bus.ErrNotFound) {
			return fmt.Errorf("no thread or message %q", pos[0])
		} else if err != nil {
			return err
		}
		if msgs, err = st.TrackThread(ctx, m.ThreadID); err != nil {
			return err
		}
	}
	agents, err := bus.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))).AgentsWithRecentSessions(ctx)
	if err != nil {
		return err
	}
	printThread(os.Stdout, agents, msgs, *full, time.Now())
	return nil
}

func printThread(w io.Writer, agents []bus.Agent, msgs []store.Tracked, full bool, now time.Time) {
	labels, kinds := labelSessions(agents), deliveryKinds(agents)
	var who []string
	for _, m := range msgs {
		for _, name := range []string{m.From, m.To} {
			if !slices.Contains(who, name) {
				who = append(who, name)
			}
		}
	}
	fmt.Fprintf(w, "Thread %s: %d %s between %s.\n", printable(msgs[0].ThreadID, false), len(msgs), plural(len(msgs), "message", "messages"), joinAnd(who))
	cut := false
	for _, m := range msgs {
		fmt.Fprintf(w, "\n── %s  %s  %s → %s\n", m.ID, m.CreatedAt.Local().Format(time.DateTime), labels.of(m.From, m.FromSession), labels.of(m.To, m.ToSession))
		fmt.Fprintf(w, "   Status: %s\n", progress(m, kinds[m.To], labels, now))
		text := printable(strings.TrimSpace(m.Content), true)
		if !full {
			var clipped bool
			if text, clipped = clipLines(text, 6, 500); clipped {
				cut = true
			}
		}
		for line := range strings.Lines(text) {
			fmt.Fprintf(w, "   %s", line)
		}
		fmt.Fprintln(w)
	}
	if cut {
		fmt.Fprintf(w, "\nSome messages were cut short. To see them in full: birdie thread %s --full\n", printable(msgs[0].ThreadID, false))
	}
}

// progress says how far a message has got. kind is its recipient's delivery.
func progress(m store.Tracked, kind string, labels sessionLabels, now time.Time) string {
	taker := ""
	if m.TakenBy != "" {
		taker = labels.of(m.To, m.TakenBy)
	}
	switch m.Status {
	case bus.StatusQueued:
		if m.Error == "" {
			return "queued"
		}
		s := fmt.Sprintf("queued; try %d failed %s: %s", m.Attempts, ago(now, m.ErrorAt), errorText(m.Error, 200))
		switch {
		case m.Retry.IsZero():
		case m.Retry.After(now):
			s += "; next try in " + span(m.Retry.Sub(now))
		default:
			s += "; next try is due"
		}
		return s
	case bus.StatusClaimed:
		if kind == "acp" {
			return "being answered"
		}
		if taker != "" {
			return "taken by " + taker + ", not confirmed yet"
		}
		return "taken, not confirmed yet"
	case bus.StatusDelivered, bus.StatusReplied:
		s := "delivered"
		if m.Status == bus.StatusReplied {
			s = "answered; delivered"
		}
		if !m.Delivered.IsZero() {
			s += " after " + span(m.Delivered.Sub(m.CreatedAt))
		}
		if taker != "" {
			s += " to " + taker
		}
		return s
	case bus.StatusFailed:
		return fmt.Sprintf("not delivered: birdie gave up after %d %s: %s", m.Attempts, plural(m.Attempts, "try", "tries"), errorText(m.Error, 200))
	}
	return m.Status
}

// sessionLabels names sessions by their address, keyed by agent/id.
type sessionLabels map[string]string

func labelSessions(agents []bus.Agent) sessionLabels {
	l := sessionLabels{}
	for _, a := range agents {
		for _, s := range a.Sessions {
			l[a.Name+"/"+s.ID] = sessionAddress(s)
		}
	}
	return l
}

// of names one of agent's sessions, or the agent itself when id is empty.
func (l sessionLabels) of(agent, id string) string {
	if id == "" {
		return agent
	}
	if addr, ok := l[agent+"/"+id]; ok {
		return addr
	}
	if len(id) > 8 {
		id = id[:8]
	}
	return agent + "/" + printable(id, false)
}

func deliveryKinds(agents []bus.Agent) map[string]string {
	kinds := map[string]string{}
	for _, a := range agents {
		kinds[a.Name] = a.Delivery
	}
	return kinds
}

// clipLines shortens text to at most n lines and chars characters.
func clipLines(text string, n, chars int) (string, bool) {
	var b strings.Builder
	lines := 0
	for line := range strings.Lines(text) {
		if lines == n {
			return strings.TrimRight(b.String(), "\n") + " …", true
		}
		b.WriteString(line)
		lines++
	}
	out := b.String()
	if clipped := clip(out, chars); clipped != out {
		return clipped, true
	}
	return out, false
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return "nobody"
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
