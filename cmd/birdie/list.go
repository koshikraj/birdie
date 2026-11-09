package main

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

const (
	// problemWindow is how far back status counts messages birdie gave up on.
	problemWindow = 24 * time.Hour
)

// printSessions lists the agents' sessions, running ones first. Sessions that
// stopped are listed when all is set, and counted otherwise.
func printSessions(w io.Writer, agents []bus.Agent, all bool, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tADDRESS\tSTATE\tTIER\tDEFAULT\tBRANCH\tFOLDER")
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
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", sessionLabel(a, s), sessionAddress(s), sessionState(s, now), s.Tier, def,
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
	header := "AGENT\tADDRESS\tDELIVERY\tSESSIONS\tWAITING\tLAST DELIVERED"
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
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s", printable(a.DisplayName(), false), a.Name, a.Delivery, sessions, waiting, ago(now, d.Delivered))
		if describe {
			row += "\t" + printable(a.Description, false)
		}
		fmt.Fprintln(tw, row)
		problems = append(problems, deliveryProblems(printable(a.DisplayName(), false), d, live, now)...)
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
