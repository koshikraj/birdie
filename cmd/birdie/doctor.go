package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"birdie/internal/bus"
	"birdie/internal/client"
	"birdie/internal/store"
)

const defaultURL = "http://127.0.0.1:7777"

type checkLevel int

const (
	checkOK checkLevel = iota
	checkWarn
	checkFail
)

type check struct {
	level checkLevel
	text  string
}

func doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	db := fs.String("db", defaultDB(), "SQLite database path")
	url := fs.String("url", envOr("BIRDIE_URL", defaultURL), "birdie base URL")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	checks := runChecks(ctx, *db, *url, home, newServiceManager())
	printChecks(os.Stdout, checks)
	for _, c := range checks {
		if c.level == checkFail {
			return errors.New("some checks failed")
		}
	}
	return nil
}

func printChecks(out io.Writer, checks []check) {
	marks := map[checkLevel]string{checkOK: "✓", checkWarn: "!", checkFail: "✗"}
	for _, c := range checks {
		fmt.Fprintf(out, "  %s %s\n", marks[c.level], c.text)
	}
}

// runChecks reports on the bus, the service and every agent. It never includes
// a token, a key or a full webhook URL in its text.
func runChecks(ctx context.Context, db, url, home string, svc serviceManager) []check {
	var out []check
	add := func(level checkLevel, format string, args ...any) {
		out = append(out, check{level, fmt.Sprintf(format, args...)})
	}

	if _, err := exec.LookPath("birdie"); err != nil {
		add(checkWarn, "birdie isn't on your PATH; add its folder so you can run it by name")
	}
	up := busUp(ctx, url)
	if up {
		add(checkOK, "bus is running at %s", url)
	} else {
		add(checkFail, "bus isn't reachable at %s; start it with: birdie service install, or birdie serve", url)
	}
	if svc.available() {
		switch installed, active := svc.state(); {
		case active:
			add(checkOK, "background service is running (%s)", svc.name())
		case installed:
			add(checkWarn, "background service is installed but not running; see: birdie service status")
		default:
			add(checkWarn, "no background service, so the bus stops when its terminal closes; add one with: birdie service install")
		}
	}

	st, err := store.Open(db)
	if err != nil {
		add(checkFail, "can't open the database: %v", err)
		return out
	}
	defer st.Close()
	agents, err := st.ListAgents(ctx)
	if err != nil {
		add(checkFail, "can't read agents: %v", err)
		return out
	}
	byName := map[string]bus.Agent{}
	for _, a := range agents {
		byName[a.Name] = a
	}

	if up {
		staleChecked := false
		for _, a := range agents {
			if staleChecked {
				break
			}
			if token, err := readToken(a.Name); err == nil {
				_, err := client.New(url, token).Unread(ctx)
				var api *client.APIError
				if errors.As(err, &api) && api.Status == http.StatusNotFound {
					add(checkFail, "the running bus is older than this program; restart it")
				}
				staleChecked = err == nil || api != nil
			}
		}
	}

	checked := map[string]bool{}
	for _, name := range setupOrder {
		tool := setupTools[name]
		agent, connected := connectedAs(name, home)
		if !connected {
			if _, err := exec.LookPath(name); err == nil {
				add(checkWarn, "%s is installed but not connected; run: birdie onboard", tool.label)
			}
			continue
		}
		checked[agent] = true
		if usesOldName(name, home) {
			add(checkWarn, "%s is still set up under the old name agent-bus; run: birdie onboard", tool.label)
		}
		a, ok := byName[agent]
		if !ok {
			add(checkFail, "%s is set to use agent %s, which isn't registered; run: birdie setup %s", tool.label, agent, name)
			continue
		}
		if msg := agentProblem(ctx, a, url, up); msg != "" {
			add(checkFail, "%s (agent %s): %s", tool.label, agent, msg)
			continue
		}
		add(checkOK, "%s is connected as %s, %s", tool.label, agent, sessionCount(ctx, st, agent))
	}

	for _, a := range agents {
		if checked[a.Name] {
			continue
		}
		switch a.Delivery {
		case bus.DeliveryPull:
			if a.Name == botAgent {
				add(checkWarn, "%s is waiting for its webhook URL and key; messages to it wait too. Run: birdie setup %s", a.DisplayName(), a.Name)
			}
		case "webhook":
			if msg := agentProblem(ctx, a, url, up); msg != "" {
				add(checkFail, "%s: %s", a.DisplayName(), msg)
			} else {
				add(checkOK, "%s delivers by webhook to %s; its routine can't be checked from here", a.DisplayName(), urlHost(a.Config["url"]))
			}
		case "acp":
			if msg := agentProblem(ctx, a, url, up); msg != "" {
				add(checkFail, "%s: %s", a.DisplayName(), msg)
			} else {
				add(checkOK, "%s answers headlessly with `%s`", a.DisplayName(), a.Config["command"])
			}
		}
	}
	return out
}

// agentProblem returns what stops this agent from working, or "".
func agentProblem(ctx context.Context, a bus.Agent, url string, up bool) string {
	switch a.Delivery {
	case "webhook":
		if f := a.Config["auth_file"]; f != "" {
			if b, err := os.ReadFile(f); err != nil {
				return "can't read its key file " + f
			} else if strings.TrimSpace(string(b)) == "" {
				return "its key file is empty: " + f
			}
		}
	case "acp":
		argv := strings.Fields(a.Config["command"])
		if len(argv) == 0 {
			return "it has no command"
		}
		if _, err := exec.LookPath(argv[0]); err != nil {
			return argv[0] + " isn't on PATH"
		}
		if cwd := a.Config["cwd"]; cwd != "" {
			if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
				return "its folder is missing: " + cwd
			}
		}
	}
	token, err := readToken(a.Name)
	if err != nil {
		return "its token file is missing"
	}
	if up {
		var api *client.APIError
		if _, err := client.New(url, token).Me(ctx); errors.As(err, &api) && api.Status == http.StatusUnauthorized {
			return "the bus rejects its token"
		}
	}
	return ""
}

func readToken(agent string) (string, error) {
	b, err := os.ReadFile(filepath.Join(tokenDir(), agent))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func sessionCount(ctx context.Context, st *store.SQLite, agent string) string {
	live, err := st.LiveSessions(ctx, agent, time.Now().Add(-bus.SessionTTL))
	if err != nil {
		return "sessions unknown"
	}
	switch len(live) {
	case 0:
		return "no open sessions"
	case 1:
		return "1 open session"
	}
	return fmt.Sprintf("%d open sessions", len(live))
}

func busUp(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// waitBus waits a few seconds for a service that was just started.
func waitBus(ctx context.Context, url string) bool {
	for range 25 {
		if busUp(ctx, url) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
	return false
}

func urlHost(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" {
		return "an invalid address"
	}
	return u.Host
}

// connectedAs says whether a tool's config points at birdie, and as which
// agent. It also counts an entry from before the rename.
func connectedAs(tool, home string) (string, bool) {
	if agent, ok := serverAgent(tool, home, "birdie"); ok {
		return agent, true
	}
	return serverAgent(tool, home, legacyServer)
}

// usesOldName says whether a tool's config still has its pre-rename entry.
func usesOldName(tool, home string) bool {
	_, ok := serverAgent(tool, home, legacyServer)
	return ok
}

func serverAgent(tool, home, server string) (string, bool) {
	def := setupTools[tool].agent
	switch tool {
	case "claude":
		b, err := os.ReadFile(filepath.Join(envHome("CLAUDE_CONFIG_DIR", home), ".claude.json"))
		if err != nil {
			return "", false
		}
		var cfg struct {
			MCPServers map[string]struct {
				Args []string `json:"args"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(b, &cfg) != nil {
			return "", false
		}
		entry, ok := cfg.MCPServers[server]
		if !ok {
			return "", false
		}
		for i, arg := range entry.Args {
			if arg == "--as" && i+1 < len(entry.Args) {
				return entry.Args[i+1], true
			}
		}
		return def, true
	case "codex":
		return tomlConnected(filepath.Join(envHome("CODEX_HOME", filepath.Join(home, ".codex")), "config.toml"), server, def)
	case "grok":
		return tomlConnected(filepath.Join(envHome("GROK_HOME", filepath.Join(home, ".grok")), "config.toml"), server, def)
	case "hermes":
		// A plugin, which has no old-name entry to find.
		if server != legacyServer {
			if s, ok := readHermesSettings(hermesHome(home)); ok && s.Agent != "" {
				return s.Agent, true
			}
		}
	}
	return "", false
}

var tomlAgent = regexp.MustCompile(`"--as"\s*,\s*"([^"]+)"`)

func tomlConnected(path, server, def string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	in, found := false, false
	for line := range strings.Lines(string(b)) {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			in = strings.Trim(t, "[] ") == "mcp_servers."+server
			found = found || in
			continue
		}
		if in {
			if m := tomlAgent.FindStringSubmatch(t); m != nil {
				return m[1], true
			}
		}
	}
	return def, found
}
