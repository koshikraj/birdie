package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

// waitingShown is how many waiting messages status lists.
const waitingShown = 10

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
