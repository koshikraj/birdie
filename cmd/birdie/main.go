// Command birdie is a local message bus that lets agents (Claude Code,
// Cursor, scripts, remote bots) talk to each other in both directions.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"birdie/internal/adapter"
	"birdie/internal/bus"
	"birdie/internal/client"
	"birdie/internal/httpapi"
	"birdie/internal/mcpserver"
	"birdie/internal/store"
)

var version = "dev"

const usage = `birdie — two-way messaging between agents

Usage:
  birdie serve [--addr 127.0.0.1:7777] [--db PATH] [--public-url URL] [--loop-limit 12]
  birdie onboard [--db PATH] [--url URL]
  birdie doctor [--db PATH] [--url URL]
  birdie setup claude|codex|grok|hermes|--all [--as NAME] [--remove] [--dry-run] [--wake]
  birdie setup grok-bot [--webhook URL --auth-file PATH] [--remove] [--dry-run]
  birdie service install|remove|status [--db PATH]
  birdie mcp --as NAME [--no-push] [--channel [--server-name birdie]] [--url URL]
  birdie agent add NAME [--description TEXT] [--webhook URL [--auth-file PATH] [--auth-header H]]
  birdie agent add NAME --acp "COMMAND" [--cwd DIR] [--permission reject|allow] [--timeout 20m] [--max-replies 10]
  birdie agent describe NAME TEXT
  birdie agent list
  birdie agent rm NAME
  birdie agent prompt grok-bot [--save] [--url URL]
  birdie session list [--all]
  birdie session default AGENT [NAME]
  birdie send --as NAME --to AGENT[/SESSION] [--thread ID] [--context TEXT] [--wanted TEXT] MESSAGE
  birdie reply --as NAME MSG_ID MESSAGE
  birdie inbox --as NAME [--wait 30s] [--thread ID]
  birdie version

Each agent's token is stored in %s/<name> (mode 0600).
Environment: BIRDIE_URL, BIRDIE_DB, BIRDIE_NAME, BIRDIE_TOKEN.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, tokenDir())
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := os.Args[1], os.Args[2:]
	if cmd != "version" && cmd != "help" && cmd != "-h" && cmd != "--help" {
		migrateConfig()
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, args)
	case "mcp":
		err = runMCP(ctx, args)
	case "onboard":
		err = onboard(ctx, args)
	case "doctor":
		err = doctor(ctx, args)
	case "setup":
		err = setup(ctx, args)
	case "service":
		err = serviceCmd(ctx, args)
	case "agent":
		err = agentCmd(ctx, args)
	case "session":
		err = session(ctx, args)
	case "hook":
		err = hook(ctx, args)
	case "send":
		err = send(ctx, args)
	case "reply":
		err = reply(ctx, args)
	case "inbox":
		err = inbox(ctx, args)
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Printf(usage, tokenDir())
	default:
		err = fmt.Errorf("unknown command %q (try birdie help)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "birdie:", err)
		os.Exit(1)
	}
}

// migrateConfig moves the config folder from the project's old name. The link
// left behind keeps running programs from before the rename working.
func migrateConfig() {
	base, err := os.UserConfigDir()
	if err != nil {
		return
	}
	dir, old := filepath.Join(base, "birdie"), filepath.Join(base, "agent-bus")
	if _, err := os.Lstat(dir); err == nil {
		return
	}
	if fi, err := os.Lstat(old); err != nil || !fi.IsDir() {
		return
	}
	if err := os.Rename(old, dir); err != nil {
		fmt.Fprintf(os.Stderr, "birdie: couldn't move %s to %s: %v\n", old, dir, err)
		return
	}
	os.Symlink(dir, old) // not possible everywhere; only older programs need it
	fmt.Fprintf(os.Stderr, "birdie: moved your data from %s to %s\n", old, dir)
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "birdie")
}

func tokenDir() string { return filepath.Join(configDir(), "tokens") }

func defaultDB() string { return envOr("BIRDIE_DB", filepath.Join(configDir(), "bus.db")) }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parse lets flags and positional arguments appear in any order.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "listen address")
	db := fs.String("db", defaultDB(), "SQLite database path")
	publicURL := fs.String("public-url", "", "externally reachable base URL (e.g. a tunnel), advertised to webhook agents for replies")
	loopLimit := fs.Int("loop-limit", bus.DefaultLoopLimit, "messages one agent may send another in 10 minutes before they're refused as a loop (0 = no limit)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if host, _, err := net.SplitHostPort(*addr); err == nil {
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			logger.Warn("listening on a non-loopback address; every agent token is a credential for this bus", "addr", *addr)
		}
	}

	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	acp := &adapter.ACP{State: st, Log: logger, Version: version}
	b := bus.New(st, logger, &adapter.Webhook{HTTP: &http.Client{}, PublicURL: *publicURL}, acp)
	b.LoopLimit = *loopLimit
	if *loopLimit == 0 {
		b.LoopLimit = -1
	}
	acp.Bus = b
	acp.Start(ctx)
	go b.RunDelivery(ctx)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.New(b, logger),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      bus.MaxWait + 30*time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	logger.Info("birdie listening", "addr", *addr, "db", *db, "version", version)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// agentClient builds an API client for the agent named by --as.
func agentClient(fs *flag.FlagSet, args []string) (*client.Client, string, []string, error) {
	as := fs.String("as", os.Getenv("BIRDIE_NAME"), "agent name to act as")
	url := fs.String("url", envOr("BIRDIE_URL", "http://127.0.0.1:7777"), "birdie base URL")
	tokenFile := fs.String("token-file", "", "token file (default: token dir/<name>)")
	pos, err := parse(fs, args)
	if err != nil {
		return nil, "", nil, err
	}
	if *as == "" {
		return nil, "", nil, errors.New("--as NAME is required (or set BIRDIE_NAME)")
	}
	token := os.Getenv("BIRDIE_TOKEN")
	if token == "" {
		path := *tokenFile
		if path == "" {
			path = filepath.Join(tokenDir(), *as)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, "", nil, fmt.Errorf("read token for %q: %w (create it with: birdie agent add %s)", *as, err, *as)
		}
		token = strings.TrimSpace(string(b))
	}
	return client.New(*url, token), *as, pos, nil
}

func runMCP(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	channel := fs.Bool("channel", false, "if a Claude Code session has no inbox socket, push through a development channel instead")
	serverName := fs.String("server-name", "birdie", "this server's name in the Claude Code MCP config, as used in server:NAME")
	noPush := fs.Bool("no-push", false, "don't put incoming messages into the session; they wait for check_inbox")
	c, name, _, err := agentClient(fs, args)
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "birdie mcp: ", log.LstdFlags)
	if *noPush {
		*channel = false
	}
	if *channel {
		// A channel only shows messages in a session started with the flag.
		if enabled, found := mcpserver.ChannelLaunch(*serverName); found && !enabled {
			*channel = false
		}
	}
	srv := mcpserver.New(c, name, version, *channel, logger)
	srv.DetectHost = func(client string) mcpserver.Host {
		return mcpserver.DetectHost(client, mcpserver.HostOptions{NoPush: *noPush})
	}
	return srv.Run(ctx, os.Stdin, os.Stdout)
}

func session(ctx context.Context, args []string) error {
	st, err := store.Open(defaultDB())
	if err != nil {
		return err
	}
	defer st.Close()
	b := bus.New(st, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return sessionCmd(ctx, b, func(agent, name string) error {
		if err := st.SetAgentConfig(ctx, agent, "default_session", name); err != nil {
			return fmt.Errorf("set default for %q: %w", agent, err)
		}
		if name == "" {
			fmt.Printf("%s: new conversations go to the most recently active session\n", agent)
		} else {
			fmt.Printf("%s: new conversations go to the session named %s when it's running\n", agent, name)
		}
		return nil
	}, args)
}

func send(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	to := fs.String("to", "", "recipient agent")
	replyTo := fs.String("reply-to", "", "message_id being answered")
	thread := fs.String("thread", "", "thread to continue")
	ctxText := fs.String("context", "", "supporting context")
	wanted := fs.String("wanted", "", "result wanted back")
	c, _, pos, err := agentClient(fs, args)
	if err != nil {
		return err
	}
	meta := map[string]string{}
	if *ctxText != "" {
		meta["context"] = *ctxText
	}
	if *wanted != "" {
		meta["wanted"] = *wanted
	}
	m, err := c.Send(ctx, bus.SendInput{To: *to, Content: strings.Join(pos, " "), InReplyTo: *replyTo, ThreadID: *thread, Meta: meta})
	if err != nil {
		return err
	}
	fmt.Printf("sent %s (thread %s)\n", m.ID, m.ThreadID)
	if m.Note != "" {
		fmt.Println(m.Note)
	}
	return nil
}

func reply(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	c, _, pos, err := agentClient(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return errors.New("usage: birdie reply --as NAME MSG_ID MESSAGE")
	}
	m, err := c.Send(ctx, bus.SendInput{InReplyTo: pos[0], Content: strings.Join(pos[1:], " ")})
	if err != nil {
		return err
	}
	fmt.Printf("replied to %s with %s (thread %s)\n", m.To, m.ID, m.ThreadID)
	if m.Note != "" {
		fmt.Println(m.Note)
	}
	return nil
}

func inbox(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	wait := fs.Duration("wait", 0, "long-poll up to this long if empty (max 2m)")
	thread := fs.String("thread", "", "only this thread")
	c, _, _, err := agentClient(fs, args)
	if err != nil {
		return err
	}
	msgs, err := c.Inbox(ctx, *thread, *wait)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		fmt.Printf("── %s  from %s  thread %s  %s\n%s\n", m.ID, m.From, m.ThreadID, m.CreatedAt.Local().Format(time.DateTime), m.Content)
	}
	if len(msgs) == 0 {
		fmt.Println("(no new messages)")
	}
	return nil
}

func agentCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: birdie agent add|list|rm|prompt")
	}
	fs := flag.NewFlagSet("agent "+args[0], flag.ContinueOnError)
	db := fs.String("db", defaultDB(), "SQLite database path")
	webhook := fs.String("webhook", "", "deliver to this URL instead of an inbox the agent polls")
	authFile := fs.String("auth-file", "", "file holding the webhook's secret")
	authHeader := fs.String("auth-header", "", "header for the secret (default Authorization: Bearer)")
	description := fs.String("description", "", "what this agent is for, shown to other agents")
	acpCmd := fs.String("acp", "", `run this ACP agent command for each message (e.g. "grok agent stdio")`)
	cwd := fs.String("cwd", "", "working directory for the ACP agent (default: home directory)")
	permission := fs.String("permission", "reject", "ACP tool calls that ask for approval: reject or allow")
	timeout := fs.Duration("timeout", 0, "longest an ACP turn may run (default 20m)")
	maxReplies := fs.Int("max-replies", -1, "ACP answers per thread before the agent stops replying (default 10, 0 = no limit)")
	url := fs.String("url", envOr("BIRDIE_URL", defaultURL), "birdie base URL to put in the prompt")
	save := fs.Bool("save", false, "save the prompt to a private file instead of printing it")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	switch args[0] {
	case "list":
		agents, err := bus.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))).Agents(ctx)
		if err != nil {
			return err
		}
		deliveries, err := st.Deliveries(ctx, time.Now().Add(-problemWindow))
		if err != nil {
			return err
		}
		return printAgents(os.Stdout, agents, deliveries, true, time.Now())
	case "describe":
		if len(pos) < 2 {
			return errors.New("usage: birdie agent describe NAME TEXT")
		}
		if err := st.SetAgentConfig(ctx, pos[0], "description", strings.Join(pos[1:], " ")); err != nil {
			return fmt.Errorf("describe %q: %w", pos[0], err)
		}
		fmt.Printf("updated %s\n", pos[0])
		return nil
	case "rm":
		if len(pos) != 1 {
			return errors.New("usage: birdie agent rm NAME")
		}
		if err := st.DeleteAgent(ctx, pos[0]); err != nil {
			return fmt.Errorf("remove %q: %w", pos[0], err)
		}
		os.Remove(filepath.Join(tokenDir(), pos[0]))
		if pos[0] == botAgent {
			os.Remove(botPromptPath()) // it holds the token just deleted
		}
		fmt.Printf("removed %s\n", pos[0])
		return nil
	case "prompt":
		if len(pos) != 1 || pos[0] != botAgent {
			return errors.New("usage: birdie agent prompt " + botAgent + " [--save]")
		}
		if _, err := st.GetAgent(ctx, botAgent); err != nil {
			return fmt.Errorf("%s isn't set up yet; run: birdie setup %s", botAgent, botAgent)
		}
		prompt, err := botPrompt(*url)
		if err != nil {
			return err
		}
		if !*save {
			fmt.Println(prompt)
			return nil
		}
		path, err := saveBotPrompt(prompt)
		if err != nil {
			return err
		}
		fmt.Printf("saved to %s\n", path)
		return nil
	case "add":
		if len(pos) != 1 || !bus.ValidName(pos[0]) {
			return errors.New("usage: birdie agent add NAME (lowercase letters, digits, . _ -)")
		}
		name := pos[0]
		a := bus.Agent{Name: name, Delivery: bus.DeliveryPull, CreatedAt: time.Now().UTC()}
		if *webhook != "" && *acpCmd != "" {
			return errors.New("use either --webhook or --acp, not both")
		}
		if *acpCmd != "" {
			argv := strings.Fields(*acpCmd)
			if len(argv) == 0 {
				return errors.New("--acp needs a command")
			}
			if _, err := exec.LookPath(argv[0]); err != nil {
				return fmt.Errorf("acp command: %w", err)
			}
			if *permission != "reject" && *permission != "allow" {
				return errors.New("--permission must be reject or allow")
			}
			a.Delivery = "acp"
			a.Config = map[string]string{"command": *acpCmd, "permission": *permission}
			if *cwd != "" {
				abs, err := filepath.Abs(*cwd)
				if err != nil {
					return err
				}
				if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
					return fmt.Errorf("--cwd %s is not a directory", abs)
				}
				a.Config["cwd"] = abs
			}
			if *timeout > 0 {
				a.Config["timeout"] = timeout.String()
			}
			if *maxReplies >= 0 {
				a.Config["max_replies"] = strconv.Itoa(*maxReplies)
			}
		}
		if *webhook != "" {
			a.Delivery = "webhook"
			a.Config = map[string]string{"url": *webhook}
			if *authFile != "" {
				abs, err := filepath.Abs(*authFile)
				if err != nil {
					return err
				}
				if _, err := os.Stat(abs); err != nil {
					return fmt.Errorf("auth file: %w", err)
				}
				a.Config["auth_file"] = abs
			}
			if *authHeader != "" {
				a.Config["auth_header"] = *authHeader
			}
		}
		if *description != "" {
			if a.Config == nil {
				a.Config = map[string]string{}
			}
			a.Config["description"] = *description
		}
		if err := createAgent(ctx, st, a); err != nil {
			return err
		}
		path := filepath.Join(tokenDir(), name)
		fmt.Printf("added %s (%s delivery); token saved to %s\n", name, a.Delivery, path)
		return nil
	default:
		return fmt.Errorf("unknown agent subcommand %q", args[0])
	}
}

// createAgent registers an agent and saves its token.
func createAgent(ctx context.Context, st *store.SQLite, a bus.Agent) error {
	token := bus.NewToken()
	a.TokenHash = bus.HashToken(token)
	if err := os.MkdirAll(tokenDir(), 0o700); err != nil {
		return err
	}
	path := filepath.Join(tokenDir(), a.Name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("token file %s already exists; remove the agent first", path)
	}
	if err := st.CreateAgent(ctx, a); err != nil {
		return fmt.Errorf("add %q: %w", a.Name, err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		st.DeleteAgent(ctx, a.Name)
		return err
	}
	return nil
}
