package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"birdie/internal/bus"
	"birdie/internal/mcpserver"
)

// hook is run by an agent's hook system: it prints waiting messages in the form
// that agent puts in front of the model. It never fails loudly, because a
// broken bus must not break the agent.
func hook(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c, _, pos, err := agentClient(fs, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "birdie hook:", err)
		return nil
	}
	if len(pos) != 2 || pos[0] != "grok" {
		fmt.Fprintln(os.Stderr, "birdie hook: usage: birdie hook grok post-tool-use|stop --as NAME")
		return nil
	}
	input, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	var in grokHookInput
	json.Unmarshal(input, &in)
	if in.ignored(pos[1]) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c.SetSession(in.SessionID)
	msgs, err := c.Inbox(ctx, "", 0)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	text := make([]string, len(msgs))
	for i, m := range msgs {
		text[i] = mcpserver.MessageText(m)
	}
	out, err := grokHookOutput(pos[1], strings.Join(text, "\n\n"))
	if err == nil {
		_, err = fmt.Println(out)
	}
	// Only what was printed counts as seen; the rest comes back when its lease ends.
	for _, m := range msgs {
		if err == nil {
			err = c.Ack(ctx, m.ID)
		} else {
			c.Nack(ctx, m.ID)
		}
	}
	return nil
}

type grokHookInput struct {
	SessionID    string  `json:"session_id"`
	SubagentType *string `json:"subagentType"`
	Reason       string  `json:"reason"`
}

// ignored skips what can't take a message: Grok runs a subagent's hooks under
// the subagent's own session, and after a session ends fires one more Stop whose
// answer it ignores.
func (in grokHookInput) ignored(event string) bool {
	return in.SessionID == "" || in.SubagentType != nil || (event == "stop" && in.Reason != "end_turn")
}

func grokHookOutput(event, text string) (string, error) {
	var v any
	switch event {
	case "post-tool-use":
		v = map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "PostToolUse", "additionalContext": text}}
	case "stop":
		v = map[string]string{"decision": "block", "reason": text}
	default:
		return "", errors.New("unknown hook event " + event)
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// sessionCmd looks at running sessions and chooses which takes new conversations.
func sessionCmd(ctx context.Context, b *bus.Bus, setDefault func(agent, name string) error, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: birdie session list [--all] | default AGENT [NAME]")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("session list", flag.ContinueOnError)
		all := fs.Bool("all", false, "also list sessions that stopped in the last day")
		if _, err := parse(fs, args[1:]); err != nil {
			return err
		}
		agents, err := b.AgentsWithRecentSessions(ctx)
		if err != nil {
			return err
		}
		return printSessions(os.Stdout, agents, *all, time.Now())
	case "default":
		if len(args) < 2 || len(args) > 3 {
			return errors.New("usage: birdie session default AGENT [NAME] (no NAME goes back to the most recently active session)")
		}
		name := ""
		if len(args) == 3 {
			name = args[2]
		}
		return setDefault(args[1], name)
	}
	return fmt.Errorf("unknown session subcommand %q", args[0])
}
