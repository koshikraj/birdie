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
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"birdie/internal/bus"
	"birdie/internal/store"
)

const (
	// problemWindow is how far back status counts messages birdie gave up on.
	problemWindow = 24 * time.Hour
	waitingShown  = 10
)

// statusCmd shows at a glance whether birdie is running, what each session
// is doing, how delivery to each agent is going, and what is still waiting.
func statusCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	db := fs.String("db", defaultDB(), "SQLite database path")
	url := fs.String("url", envOr("BIRDIE_URL", defaultURL), "birdie base URL")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	if busUp(ctx, *url) {
		fmt.Printf("birdie is running at %s.\n\n", *url)
	} else {
		fmt.Printf("birdie isn't running at %s, so nothing is being delivered. Start it with: birdie service install, or birdie serve\n\n", *url)
	}
	agents, err := bus.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))).AgentsWithRecentSessions(ctx)
	if err != nil {
		return err
	}
	deliveries, err := st.Deliveries(ctx, time.Now().Add(-problemWindow))
	if err != nil {
		return err
	}
	waiting, total, err := st.Waiting(ctx, waitingShown)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := printSessions(os.Stdout, agents, false, now); err != nil {
		return err
	}
	fmt.Println()
	if err := printAgents(os.Stdout, agents, deliveries, false, now); err != nil {
		return err
	}
	fmt.Println()
	return printWaiting(os.Stdout, agents, waiting, total, now)
}

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

// printSessions lists the agents' sessions, running ones first. Sessions that
// stopped are listed when all is set, and counted otherwise.
func printSessions(w io.Writer, agents []bus.Agent, all bool, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tSTATE\tTIER\tDEFAULT\tBRANCH\tFOLDER")
	shown, hidden := 0, 0
	for _, a := range agents {
		for _, s := range a.Sessions {
			if !all && !s.Live(now) {
				hidden++
				continue
			}
			def := ""
			if s.Default {
				def = "yes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", sessionAddress(s), sessionState(s, now), s.Tier, def,
				printable(s.Branch, false), printable(homePath(s.Cwd), false))
			shown++
		}
	}
	if shown == 0 {
		fmt.Fprintln(w, "No sessions are running.")
	} else if err := tw.Flush(); err != nil {
		return err
	}
	if hidden > 0 {
		fmt.Fprintf(w, "%d more %s stopped in the last day. To list them too: birdie session list --all\n", hidden, plural(hidden, "session", "sessions"))
	}
	return nil
}

// printAgents lists the agents with how delivery to each is going, then a
// line for each problem found.
func printAgents(w io.Writer, agents []bus.Agent, deliveries map[string]store.Delivery, describe bool, now time.Time) error {
	if len(agents) == 0 {
		_, err := fmt.Fprintln(w, "No agents yet. Add one with: birdie onboard")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	header := "AGENT\tDELIVERY\tSESSIONS\tWAITING\tLAST DELIVERED"
	if describe {
		header += "\tDESCRIPTION"
	}
	fmt.Fprintln(tw, header)
	var problems []string
	for _, a := range agents {
		d := deliveries[a.Name]
		live := -1
		sessions := "-"
		if a.Delivery == bus.DeliveryPull {
			live = 0
			for _, s := range a.Sessions {
				if s.Live(now) {
					live++
				}
			}
			sessions = strconv.Itoa(live)
		}
		waiting := "-"
		if d.Waiting > 0 {
			waiting = fmt.Sprintf("%d, oldest %s", d.Waiting, span(now.Sub(d.Oldest)))
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s", a.Name, a.Delivery, sessions, waiting, ago(now, d.Delivered))
		if describe {
			row += "\t" + printable(a.Description, false)
		}
		fmt.Fprintln(tw, row)
		problems = append(problems, deliveryProblems(a.Name, d, live, now)...)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, p := range problems {
		fmt.Fprintln(w, p)
	}
	return nil
}

// deliveryProblems explains what is going wrong with delivery to an agent.
// live is how many sessions it has running, or -1 for an agent that doesn't
// take messages through sessions.
func deliveryProblems(agent string, d store.Delivery, live int, now time.Time) []string {
	var out []string
	if d.Error != "" {
		next := "birdie stopped trying."
		switch {
		case d.Retry.IsZero():
		case d.Retry.After(now):
			next = "Next try in " + span(d.Retry.Sub(now)) + "."
		default:
			next = "Next try is due."
		}
		out = append(out, fmt.Sprintf("%s: the last delivery failed %s: %s. %s Details: birdie thread %s",
			agent, ago(now, d.ErrorAt), errorText(d.Error, 200), next, d.ErrorID))
	}
	if d.GaveUp > 0 {
		out = append(out, fmt.Sprintf("%s: %d %s couldn't be delivered in the last day.", agent, d.GaveUp, plural(d.GaveUp, "message", "messages")))
	}
	if live == 0 && d.Waiting > 0 {
		out = append(out, fmt.Sprintf("%s: no session is running, so %d %s will wait until one starts.", agent, d.Waiting, plural(d.Waiting, "message", "messages")))
	}
	return out
}

// printWaiting lists messages that haven't reached their recipient yet.
func printWaiting(w io.Writer, agents []bus.Agent, msgs []store.Tracked, total int, now time.Time) error {
	if total == 0 {
		_, err := fmt.Fprintln(w, "No messages are waiting.")
		return err
	}
	labels, kinds := labelSessions(agents), deliveryKinds(agents)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WAITING MESSAGE\tFROM\tTO\tSENT\tSTATUS\tSTARTS")
	for _, m := range msgs {
		first, _, _ := strings.Cut(strings.TrimSpace(m.Content), "\n")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.ID, labels.of(m.From, m.FromSession), labels.of(m.To, m.ToSession),
			ago(now, m.CreatedAt), clip(progress(m, kinds[m.To], labels, now), 60), clip(printable(first, false), 40))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if total > len(msgs) {
		fmt.Fprintf(w, "and %d more.\n", total-len(msgs))
	}
	_, err := fmt.Fprintln(w, "To follow one: birdie thread MSG_ID")
	return err
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

// ago says how long before now t was.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return span(now.Sub(t)) + " ago"
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

// errorText makes a delivery error fit in a sentence.
func errorText(s string, n int) string {
	return clip(strings.TrimRight(printable(strings.TrimSpace(s), false), "."), n)
}

// clip shortens s to at most n characters, marking the cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
