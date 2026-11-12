package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"birdie"
	"birdie/internal/bus"
	"birdie/internal/mcpserver"
	"birdie/internal/store"
)

// legacyServer is the project's old name. Setup replaces what it left in a
// tool's config.
const legacyServer = "agent-bus"

// setupTool wires one agent program to the bus.
type setupTool struct {
	label       string
	agent       string // default agent name
	description string
	apply       func(*setupRun) error
}

var setupTools = map[string]setupTool{
	"claude": {"Claude Code", "claude", "Claude Code on this machine: code, repos, files", setupClaude},
	"codex":  {"Codex", "codex", "Codex on this machine: code, repos, files", setupCodex},
	"grok":   {"Grok CLI", "grok-tui", "Grok CLI on this machine: coding questions and quick research", setupGrok},
	"hermes": {"Hermes", "hermes", "Hermes Agent on this machine", setupHermes},
}

// setupOrder is the order tools are listed in. Each name is also the command
// looked for on PATH.
var setupOrder = []string{"claude", "codex", "grok", "hermes"}

// setupRun carries one setup and records what it changed.
type setupRun struct {
	exe, agent string
	remove     bool
	dry        bool
	stamp      string
	home       string
	db         string
	url        string
	wake       *bool // Hermes only; nil keeps the current setting
	said       int
}

// setupOptions are the settings shared by every tool in one setup command.
type setupOptions struct {
	as, db, exe, home, stamp string
	remove, dry              bool
	wake                     *bool
	quiet                    bool // leave closing advice to the caller
}

func setup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	as := fs.String("as", "", "agent name on the bus (default: claude, codex or grok-tui); needs a tool name")
	remove := fs.Bool("remove", false, "undo the setup; the agent and its token stay")
	dry := fs.Bool("dry-run", false, "print what would change")
	all := fs.Bool("all", false, "set up every tool found on PATH, without asking")
	wake := fs.Bool("wake", false, "Hermes only: let other agents' messages start turns on their own; --wake=false turns that off")
	webhook := fs.String("webhook", "", "Grok Bot only: its routine's webhook URL")
	authFile := fs.String("auth-file", "", "Grok Bot only: file holding the webhook's key")
	db := fs.String("db", defaultDB(), "SQLite database path")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 || (len(pos) == 0) != *all {
		return errors.New("usage: birdie setup claude|codex|grok|hermes|--all [--as NAME] [--remove] [--dry-run] [--wake]\n       birdie setup grok-bot [--webhook URL --auth-file PATH] [--remove] [--dry-run]\nFor a guided setup, run: birdie onboard")
	}
	isBot := len(pos) == 1 && pos[0] == botAgent
	if !isBot && (*webhook != "" || *authFile != "") {
		return errors.New("--webhook and --auth-file only apply to: birdie setup " + botAgent)
	}
	if isBot && *as != "" {
		return errors.New("Grok Bot's agent is always named " + botAgent + ", so --as doesn't apply")
	}
	o, err := newSetupOptions(*db)
	if err != nil {
		return err
	}
	o.as, o.remove, o.dry = *as, *remove, *dry
	var wakeSet bool
	fs.Visit(func(f *flag.Flag) { wakeSet = wakeSet || f.Name == "wake" })
	if wakeSet {
		if len(pos) != 1 || pos[0] != "hermes" {
			return errors.New("--wake only applies to: birdie setup hermes")
		}
		o.wake = wake
	}
	if *all {
		return setupAll(ctx, o, os.Stdout)
	}
	if isBot {
		var p prompter
		if *webhook == "" && !o.remove && !o.dry {
			if t, closeTerminal, err := openTerminal(ctx, os.Stdout); err == nil {
				defer closeTerminal()
				p = t
			}
		}
		return setupBot(ctx, o, envOr("BIRDIE_URL", defaultURL), *webhook, *authFile, p, os.Stdout)
	}
	if _, ok := setupTools[pos[0]]; !ok {
		return fmt.Errorf("can't set up %q; choose claude, codex, grok, hermes or %s", pos[0], botAgent)
	}
	return setupOne(ctx, pos[0], o, o.dry)
}

func newSetupOptions(db string) (setupOptions, error) {
	exe, err := os.Executable()
	if err != nil {
		return setupOptions{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return setupOptions{}, err
	}
	return setupOptions{db: db, exe: exe, home: home, stamp: time.Now().Format("20060102-150405")}, nil
}

// setupOne applies or previews the setup of one tool.
func setupOne(ctx context.Context, name string, o setupOptions, dry bool) error {
	tool := setupTools[name]
	r := &setupRun{exe: o.exe, agent: tool.agent, remove: o.remove, dry: dry, stamp: o.stamp, home: o.home, db: o.db, url: envOr("BIRDIE_URL", defaultURL), wake: o.wake}
	if o.as != "" {
		r.agent = o.as
	} else if agent, ok := connectedAs(name, o.home); ok {
		r.agent = agent // keep the name it's connected under
	}
	if !o.remove && !dry {
		if err := ensureAgent(ctx, o.db, r.agent, tool.label, tool.description); err != nil {
			return err
		}
	}
	if err := tool.apply(r); err != nil {
		return err
	}
	switch {
	case dry:
		if r.said == 0 {
			fmt.Println("nothing to change")
		}
	case o.quiet:
	case o.remove:
		fmt.Printf("removed. The agent %s and its token stay; remove them with: birdie agent rm %s\n", r.agent, r.agent)
	default:
		if name == "hermes" {
			fmt.Println("done. To load it, restart Hermes with: hermes gateway restart. Keep `birdie serve` running.")
		} else {
			fmt.Printf("done. Restart any open %s sessions, and keep `birdie serve` running.\n", name)
		}
	}
	return nil
}

// setupAll sets up every tool found on PATH. A failure doesn't stop the rest.
func setupAll(ctx context.Context, o setupOptions, out io.Writer) error {
	if o.as != "" {
		return errors.New("--as names one agent, so it needs a tool name: birdie setup claude --as NAME")
	}
	if o.remove {
		return errors.New("--remove needs a tool name, so nothing is removed by accident")
	}
	found, missing := detectTools()
	if len(found) == 0 {
		return fmt.Errorf("found none of %s on PATH", strings.Join(setupOrder, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(out, "Not found on PATH: %s\n", strings.Join(missing, ", "))
	}
	var failed []string
	for _, name := range found {
		fmt.Fprintf(out, "\n%s:\n", setupTools[name].label)
		if err := setupOne(ctx, name, o, o.dry); err != nil {
			fmt.Fprintf(out, "failed: %v\n", err)
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("setup failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// detectTools splits the known tools into those on PATH and those not.
func detectTools() (found, missing []string) {
	for _, name := range setupOrder {
		if _, err := exec.LookPath(name); err == nil {
			found = append(found, name)
		} else {
			missing = append(missing, name)
		}
	}
	return found, missing
}

// ensureAgent adds the agent for a tool, or saves the tool's label with an
// agent added before birdie saved labels.
func ensureAgent(ctx context.Context, db, name, label, description string) error {
	st, err := store.Open(db)
	if err != nil {
		return err
	}
	defer st.Close()
	if a, err := st.GetAgent(ctx, name); err == nil {
		if a.Config["label"] == "" {
			return st.SetAgentConfig(ctx, name, "label", label)
		}
		return nil
	} else if !errors.Is(err, bus.ErrNotFound) {
		return err
	}
	if err := createAgent(ctx, st, bus.Agent{
		Name: name, Delivery: bus.DeliveryPull, CreatedAt: time.Now().UTC(),
		Config: map[string]string{"description": description, "label": label},
	}); err != nil {
		return err
	}
	fmt.Printf("added agent %s\n", name)
	return nil
}

func (r *setupRun) say(format string, args ...any) {
	r.said++
	prefix := ""
	if r.dry {
		prefix = "would "
	}
	fmt.Println(prefix + fmt.Sprintf(format, args...))
}

// backup copies a file about to change into the birdie config folder.
func (r *setupRun) backup(path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	dir := filepath.Join(configDir(), "backups", r.stamp)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dest := filepath.Join(dir, strings.TrimLeft(strings.ReplaceAll(strings.TrimPrefix(path, r.home), string(filepath.Separator), "_"), "_"))
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("backed up %s to %s\n", path, dest)
	return nil
}

// write replaces a file's content, with a backup, if it would change.
func (r *setupRun) write(path, content string) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && string(old) == content {
		return nil
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	r.say("write %s", path)
	if r.dry {
		return nil
	}
	if err := r.backup(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".birdie-tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (r *setupRun) removeFile(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	r.say("remove %s", path)
	if r.dry {
		return nil
	}
	if err := r.backup(path); err != nil {
		return err
	}
	return os.Remove(path)
}

func (r *setupRun) skill(dir string) error {
	path := filepath.Join(dir, "talk-to-agents", "SKILL.md")
	if r.remove {
		if err := r.removeFile(path); err != nil {
			return err
		}
		if !r.dry {
			os.Remove(filepath.Dir(path)) // only if empty
		}
		return nil
	}
	return r.write(path, birdie.Skill)
}

// tomlServer edits the birdie MCP server in a TOML config file.
func (r *setupRun) tomlServer(path string) error {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	block := ""
	if !r.remove {
		block = fmt.Sprintf(`[mcp_servers.birdie]
command = %q
args = ["mcp", "--as", %q]
enabled = true
tool_timeout_sec = 660
`, r.exe, r.agent)
	}
	text := setTOMLBlock(string(b), "mcp_servers."+legacyServer, "")
	return r.write(path, setTOMLBlock(text, "mcp_servers.birdie", block))
}

// setTOMLBlock replaces a table and its subtables, or adds it at the end. An
// empty block removes it. Everything else in the file stays as written.
func setTOMLBlock(text, table, block string) string {
	var kept []string
	skipping := false
	for _, line := range strings.SplitAfter(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			name := strings.Trim(t, "[] ")
			skipping = name == table || strings.HasPrefix(name, table+".")
		}
		if !skipping {
			kept = append(kept, line)
		}
	}
	out := strings.TrimRight(strings.Join(kept, ""), "\n")
	if out != "" {
		out += "\n"
	}
	if block != "" {
		if out != "" {
			out += "\n"
		}
		out += block
	}
	return out
}

func envHome(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fallback
}

func setupClaude(r *setupRun) error {
	dir := envHome("CLAUDE_CONFIG_DIR", filepath.Join(r.home, ".claude"))
	if err := r.skill(filepath.Join(dir, "skills")); err != nil {
		return err
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("the claude command isn't on PATH: %w", err)
	}
	r.say("update the birdie MCP server in Claude Code's user config")
	if r.dry {
		return nil
	}
	if err := r.backup(filepath.Join(envHome("CLAUDE_CONFIG_DIR", r.home), ".claude.json")); err != nil {
		return err
	}
	// Adding fails if it exists, so start clean. It's fine if there's nothing to remove.
	exec.Command("claude", "mcp", "remove", "-s", "user", legacyServer).Run()
	exec.Command("claude", "mcp", "remove", "-s", "user", "birdie").Run()
	if r.remove {
		return nil
	}
	cmd := exec.Command("claude", "mcp", "add", "-s", "user", "birdie", "--", r.exe, "mcp", "--as", r.agent)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("claude mcp add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func setupCodex(r *setupRun) error {
	dir := envHome("CODEX_HOME", filepath.Join(r.home, ".codex"))
	if err := r.skill(filepath.Join(dir, "skills")); err != nil {
		return err
	}
	return r.tomlServer(filepath.Join(dir, "config.toml"))
}

func setupGrok(r *setupRun) error {
	dir := envHome("GROK_HOME", filepath.Join(r.home, ".grok"))
	if err := r.skill(filepath.Join(dir, "skills")); err != nil {
		return err
	}
	if err := r.tomlServer(filepath.Join(dir, "config.toml")); err != nil {
		return err
	}
	path := mcpserver.GrokHooksPath()
	if err := r.removeFile(filepath.Join(filepath.Dir(path), legacyServer+".json")); err != nil {
		return err
	}
	if r.remove {
		return r.removeFile(path)
	}
	content, err := grokHooks(r.exe, r.agent)
	if err != nil {
		return err
	}
	return r.write(path, content)
}

// hermesHome is the Hermes profile setup applies to.
func hermesHome(home string) string {
	base := envHome("HERMES_HOME", "")
	if base != "" {
		return base
	}
	base = filepath.Join(home, ".hermes")
	b, err := os.ReadFile(filepath.Join(base, "active_profile"))
	active := strings.TrimSpace(string(b))
	if err == nil && active != "" && active != "default" && active != "." && active != ".." && !strings.ContainsAny(active, `/\\`) {
		if fi, err := os.Stat(filepath.Join(base, "profiles", active)); err == nil && fi.IsDir() {
			return filepath.Join(base, "profiles", active)
		}
	}
	return base
}

// hermesTokenEnv is the Hermes secret holding the agent's token. The catalog's rules
// say credentials come through Hermes, so the plugin doesn't read birdie's token files,
// and setup doesn't write Hermes's .env: Hermes stores the secret itself, when its
// install asks for it or when the user adds it. It isn't BIRDIE_TOKEN, which a birdie
// command started by Hermes would then pick up.
const hermesTokenEnv = "BIRDIE_HERMES_TOKEN"

// hermesCatalogMarker is in the plugin's folder when Hermes installed it from its
// catalog. Hermes then owns the code, and updates it to a reviewed commit.
const hermesCatalogMarker = ".hermes-catalog.json"

// hermesSettings is what the Hermes plugin reads from birdie.json.
type hermesSettings struct {
	Agent string `json:"agent"`
	URL   string `json:"url"`
	Wake  bool   `json:"wake"`
}

func readHermesSettings(root string) (hermesSettings, bool) {
	var s hermesSettings
	b, err := os.ReadFile(filepath.Join(root, "plugins", "birdie", "birdie.json"))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return s, false
	}
	return s, true
}

// setupHermes installs a native plugin rather than an MCP server: only a plugin
// knows which conversation it's in, and can start a turn in it. Hermes's own
// commands change its config, so its comments and formatting survive.
func setupHermes(r *setupRun) error {
	if _, err := exec.LookPath("hermes"); err != nil {
		return fmt.Errorf("the hermes command isn't on PATH: %w", err)
	}
	root := hermesHome(r.home)
	dir := filepath.Join(root, "plugins", "birdie")
	config := filepath.Join(root, "config.yaml")
	current, installed := readHermesSettings(root)
	files := map[string]string{"plugin.yaml": birdie.HermesManifest, "__init__.py": birdie.HermesPlugin}
	code := []string{"plugin.yaml", "__init__.py"}
	if pathExists(filepath.Join(dir, hermesCatalogMarker)) {
		code = nil
	}

	if r.remove {
		if !installed && !pathExists(dir) {
			return nil
		}
		r.say("disable the birdie plugin in Hermes, and take back its permission to start turns")
		if !r.dry {
			if err := r.backup(config); err != nil {
				return err
			}
			hermesRun("plugins", "disable", "birdie") // fine if it wasn't enabled
			hermesRun("config", "set", "plugins.entries.birdie.allow_gateway_injection", "false")
		}
		if !r.dry {
			// Hermes keeps the secret, so Hermes clears it.
			fmt.Printf("Hermes still has the token as %s. To clear it, run: hermes config unset %s\n", hermesTokenEnv, hermesTokenEnv)
		}
		for _, name := range append(code, "birdie.json") {
			if err := r.removeFile(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
		if code == nil {
			r.say("leave the plugin's code, which Hermes installed; remove it with: hermes plugins remove birdie")
		} else if !r.dry {
			os.RemoveAll(filepath.Join(dir, "__pycache__"))
			os.Remove(dir) // only if empty
		}
		return nil
	}

	wake := current.Wake
	if r.wake != nil {
		wake = *r.wake
	}
	settings, err := json.MarshalIndent(hermesSettings{Agent: r.agent, URL: r.url, Wake: wake}, "", "  ")
	if err != nil {
		return err
	}
	for _, name := range code {
		if err := r.write(filepath.Join(dir, name), files[name]); err != nil {
			return err
		}
	}
	if err := r.write(filepath.Join(dir, "birdie.json"), string(settings)+"\n"); err != nil {
		return err
	}
	servers := hermesMCPServers(config)
	r.say("enable the birdie plugin in Hermes")
	r.say("check that Hermes has the token of agent %s as %s, and tell you how to give it if not", r.agent, hermesTokenEnv)
	for _, name := range servers {
		r.say("remove the %s MCP server from Hermes, which the plugin replaces", name)
	}
	if r.wake != nil {
		r.say("set Hermes's permission for the plugin to start turns to %v", wake)
	}
	if r.dry {
		return nil
	}
	if err := r.backup(config); err != nil {
		return err
	}
	token, err := os.ReadFile(filepath.Join(tokenDir(), r.agent))
	if err != nil {
		return fmt.Errorf("read the token of agent %s: %w", r.agent, err)
	}
	for _, name := range servers {
		if err := hermesRun("mcp", "remove", name); err != nil {
			return err
		}
	}
	if err := hermesRun("plugins", "enable", "birdie"); err != nil {
		return err
	}
	if r.wake != nil {
		if err := hermesRun("config", "set", "plugins.entries.birdie.allow_gateway_injection", fmt.Sprint(wake)); err != nil {
			return err
		}
	}
	// Hermes starts from the plugin tools it saw last time it ran, so without this
	// its first conversation wouldn't have them.
	hermesRun("tools", "list")
	if t := strings.TrimSpace(string(token)); hermesSecret(hermesTokenEnv) == t {
		fmt.Printf("Hermes already has the token of agent %s.\n", r.agent)
	} else {
		fmt.Print(hermesTokenHelp(r.agent, t))
	}
	return nil
}

// How long hermesSecret waits for Hermes, and then for anything Hermes left running
// that still holds its output open. Variables so a test can shorten them.
var (
	hermesSecretTimeout = hermesTimeout
	hermesSecretDelay   = 5 * time.Second
)

// hermesSecret asks Hermes for one of its secrets, or "" when it hasn't one. Setup
// only compares it, so it never reads or writes Hermes's .env itself.
func hermesSecret(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), hermesSecretTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "hermes", "config", "get", "--raw", name)
	cmd.WaitDelay = hermesSecretDelay // without it, a child holding the output open would hang setup
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1]) // Hermes may print progress first
}

// hermesTokenHelp tells the user how to hand the token to Hermes, which stores its
// secrets itself. The token is printed here rather than put on a command line.
func hermesTokenHelp(agent, token string) string {
	return fmt.Sprintf(`
Hermes needs the token of agent %[1]s as its %[2]s secret. Hermes stores its own
secrets and birdie doesn't touch Hermes's .env, so give it to Hermes yourself:
  - When Hermes installs the plugin (hermes plugins install birdie), paste it at
    the %[2]s prompt.
  - Otherwise add this line to Hermes's .env (hermes config env-path shows where):

    %[2]s=%[3]s

Keep it private: it lets anything holding it send and read messages as %[1]s.
Then restart Hermes.
`, agent, hermesTokenEnv, token)
}

// hermesTimeout bounds each Hermes command, so a stuck Hermes can't hang setup.
const hermesTimeout = 3 * time.Minute

func hermesRun(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), hermesTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "hermes", args...)
	cmd.Stdin = strings.NewReader("y\n") // only what setup was asked to do; it answers a confirmation
	cmd.WaitDelay = 5 * time.Second      // something it left running may hold its output open
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("hermes %s didn't finish in %s", strings.Join(args, " "), hermesTimeout)
	}
	if err != nil {
		return fmt.Errorf("hermes %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// hermesMCPServers finds birdie's own MCP entries in Hermes's config, under either
// name. Reading the YAML by line is enough: an entry is a key two spaces in.
func hermesMCPServers(config string) []string {
	b, err := os.ReadFile(config)
	if err != nil {
		return nil
	}
	var found []string
	in := false
	for line := range strings.Lines(string(b)) {
		line = strings.TrimRight(line, "\r\n")
		if line != "" && line[0] != ' ' && line[0] != '#' {
			in = strings.TrimSpace(line) == "mcp_servers:"
			continue
		}
		if in {
			for _, name := range []string{"birdie", legacyServer} {
				if line == "  "+name+":" {
					found = append(found, name)
				}
			}
		}
	}
	return found
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func grokHooks(exe, agent string) (string, error) {
	group := func(event string, matcher bool) map[string]any {
		g := map[string]any{"hooks": []map[string]any{{
			"type":    "command",
			"command": fmt.Sprintf("%s hook grok %s --as %s", shellQuote(exe), event, shellQuote(agent)),
			"timeout": 15,
		}}}
		if matcher {
			g["matcher"] = "*"
		}
		return g
	}
	b, err := json.MarshalIndent(map[string]any{"hooks": map[string]any{
		"PostToolUse": []any{group("post-tool-use", true)},
		"Stop":        []any{group("stop", false)},
	}}, "", "  ")
	return string(b) + "\n", err
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
