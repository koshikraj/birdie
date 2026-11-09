package main

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"birdie/internal/bus"
)

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
