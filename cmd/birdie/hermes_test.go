package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"birdie/internal/bus"
	"birdie/internal/httpapi"
	"birdie/internal/store"
)

// fakeHermes makes a temp home with a `hermes` command that records its arguments.
func fakeHermes(t *testing.T) (setupOptions, string) {
	t.Helper()
	o := fakeTools(t)
	bin := os.Getenv("PATH")
	log := filepath.Join(o.home, "hermes-calls")
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "hermes"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", "")
	t.Setenv("BIRDIE_URL", "")
	return o, log
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetupHermes(t *testing.T) {
	o, calls := fakeHermes(t)
	root := filepath.Join(o.home, ".hermes")
	os.MkdirAll(root, 0o700)
	config := "# keep this\nmcp_servers:\n  other:\n    command: x\n  birdie:\n    command: y\n  agent-bus:\n    command: z\nmodel:\n  birdie:\n    no: match\n"
	os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0o600)
	envPath := filepath.Join(root, ".env")
	envBefore := "# mine\nOTHER=1\n" + hermesTokenEnv + "=old\nLAST=2"
	os.WriteFile(envPath, []byte(envBefore), 0o644)

	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "plugins", "birdie")
	if !strings.Contains(read(t, filepath.Join(dir, "__init__.py")), "def register(ctx)") || !strings.Contains(read(t, filepath.Join(dir, "plugin.yaml")), "name: birdie") {
		t.Fatal("plugin files weren't installed")
	}
	settings := read(t, filepath.Join(dir, "birdie.json"))
	token := strings.TrimSpace(read(t, filepath.Join(tokenDir(), "hermes")))
	if token == "" || strings.Contains(settings, token) || strings.Contains(settings, "token") {
		t.Fatalf("birdie.json must not hold or point at the token:\n%s", settings)
	}
	if !strings.Contains(settings, defaultURL) {
		t.Fatalf("settings:\n%s", settings)
	}
	// Hermes stores its own secrets: setup only asks Hermes whether it has the token.
	if got := read(t, envPath); got != envBefore {
		t.Errorf("setup changed Hermes's .env:\n%s", got)
	}
	got := read(t, calls)
	if strings.Contains(got, token) {
		t.Error("the token must not go on a command line")
	}
	if backups, _ := filepath.Glob(filepath.Join(configDir(), "backups", "*", "*env*")); len(backups) != 0 {
		t.Errorf("a file of secrets was backed up: %v", backups)
	}
	for _, want := range []string{"mcp remove birdie", "mcp remove agent-bus", "plugins enable birdie", "tools list", "config get --raw " + hermesTokenEnv} {
		if !strings.Contains(got, want) {
			t.Errorf("calls lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "mcp remove other") || strings.Contains(got, "allow_gateway_injection") {
		t.Errorf("did too much:\n%s", got)
	}
	if a, ok := connectedAs("hermes", o.home); !ok || a != "hermes" {
		t.Errorf("connectedAs = %q, %v", a, ok)
	}
	if read(t, filepath.Join(root, "config.yaml")) != config {
		t.Error("setup must leave Hermes's config to Hermes's own commands")
	}

	// Waking is opt-in, needs Hermes's permission, and a plain rerun keeps it.
	var s hermesSettings
	readWake := func() bool {
		json.Unmarshal([]byte(read(t, filepath.Join(dir, "birdie.json"))), &s)
		return s.Wake
	}
	if readWake() {
		t.Fatal("wake must start off")
	}
	os.Remove(calls)
	on, off := true, false
	o.wake = &on
	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	if !readWake() || !strings.Contains(read(t, calls), "config set plugins.entries.birdie.allow_gateway_injection true") {
		t.Fatalf("--wake: %s", read(t, calls))
	}
	os.Remove(calls)
	o.wake = nil
	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	if !readWake() || strings.Contains(read(t, calls), "allow_gateway_injection") {
		t.Fatal("a plain rerun changed the wake setting")
	}
	o.wake = &off
	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	if readWake() || !strings.Contains(read(t, calls), "allow_gateway_injection false") {
		t.Fatal("--wake=false didn't turn it off")
	}

	// Keeps a custom name, like the other tools.
	o.wake = nil
	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	if a, _ := connectedAs("hermes", o.home); a != "hermes" {
		t.Errorf("agent = %q", a)
	}

	// Removing takes everything of ours and leaves the agent and its token.
	os.Remove(calls)
	o.remove = true
	os.WriteFile(filepath.Join(dir, "__pycache__"), nil, 0o600)
	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	got = read(t, calls)
	if !strings.Contains(got, "plugins disable birdie") || !strings.Contains(got, "allow_gateway_injection false") {
		t.Errorf("remove calls:\n%s", got)
	}
	if exists(dir) {
		t.Error("plugin folder left behind")
	}
	if _, ok := connectedAs("hermes", o.home); ok {
		t.Error("still connected")
	}
	if !exists(filepath.Join(tokenDir(), "hermes")) {
		t.Error("the token should stay")
	}
	if got := read(t, envPath); got != envBefore {
		t.Errorf("removing changed Hermes's .env; it should only say how to clear the token:\n%s", got)
	}
	os.Remove(calls)
	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	if read(t, calls) != "" {
		t.Error("removing twice shouldn't run Hermes")
	}
}

// A catalog install has Hermes's reviewed code, which setup must leave alone.
func TestSetupHermesLeavesCatalogCodeToHermes(t *testing.T) {
	o, calls := fakeHermes(t)
	dir := filepath.Join(o.home, ".hermes", "plugins", "birdie")
	os.MkdirAll(dir, 0o700)
	marker := filepath.Join(dir, hermesCatalogMarker)
	os.WriteFile(marker, []byte("{}"), 0o600)
	code := filepath.Join(dir, "__init__.py")
	os.WriteFile(code, []byte("# reviewed\n"), 0o600)

	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	if read(t, code) != "# reviewed\n" || exists(filepath.Join(dir, "plugin.yaml")) {
		t.Error("setup changed Hermes's copy of the code")
	}
	if !exists(filepath.Join(dir, "birdie.json")) || !strings.Contains(read(t, calls), "plugins enable birdie") {
		t.Error("setup should still connect it")
	}

	o.remove = true
	if err := setupOne(context.Background(), "hermes", o, false); err != nil {
		t.Fatal(err)
	}
	if read(t, code) != "# reviewed\n" || !exists(marker) {
		t.Error("removing took Hermes's copy of the code")
	}
	if exists(filepath.Join(dir, "birdie.json")) {
		t.Error("removing left birdie.json")
	}
	if _, ok := connectedAs("hermes", o.home); ok {
		t.Error("still connected")
	}
}

func TestHermesTokenHelp(t *testing.T) {
	help := hermesTokenHelp("hermes", "tok123")
	for _, want := range []string{hermesTokenEnv + "=tok123", "hermes plugins install birdie", "hermes config env-path", "agent hermes"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
}

// Setup takes Hermes's word for the secret, ignoring the progress Hermes may print first.
func TestHermesSecret(t *testing.T) {
	fakeHermes(t)
	os.WriteFile(filepath.Join(os.Getenv("PATH"), "hermes"), []byte("#!/bin/sh\n[ \"$*\" = \"config get --raw K\" ] || exit 1\necho preparing...\necho tok123\n"), 0o755)
	if got := hermesSecret("K"); got != "tok123" {
		t.Errorf("hermesSecret = %q", got)
	}
	if got := hermesSecret("OTHER"); got != "" {
		t.Errorf("a missing secret = %q", got)
	}
}

func TestSetupHermesDryRunChangesNothing(t *testing.T) {
	o, calls := fakeHermes(t)
	on := true
	o.wake = &on
	if err := setupOne(context.Background(), "hermes", o, true); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(o.home, ".hermes")) || exists(calls) {
		t.Fatal("a dry run changed something")
	}
}

func TestSetupHermesReportsAFailure(t *testing.T) {
	o, _ := fakeHermes(t)
	os.WriteFile(filepath.Join(os.Getenv("PATH"), "hermes"), []byte("#!/bin/sh\necho nope >&2\nexit 3\n"), 0o755)
	err := setupOne(context.Background(), "hermes", o, false)
	if err == nil || !strings.Contains(err.Error(), "plugins enable birdie") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetupHermesNeedsHermes(t *testing.T) {
	o := fakeTools(t)
	if err := setupOne(context.Background(), "hermes", o, false); err == nil || !strings.Contains(err.Error(), "isn't on PATH") {
		t.Fatalf("err = %v", err)
	}
}

func TestHermesHomeFollowsTheProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_HOME", "")
	base := filepath.Join(home, ".hermes")
	if got := hermesHome(home); got != base {
		t.Errorf("default = %s", got)
	}
	os.MkdirAll(filepath.Join(base, "profiles", "work"), 0o700)
	os.MkdirAll(filepath.Join(base, "other"), 0o700)
	for active, want := range map[string]string{
		"work\n":        filepath.Join(base, "profiles", "work"),
		"default":       base,
		"missing":       base,
		"../../../etc":  base,
		"..":            base,
		"work/../other": base,
	} {
		os.WriteFile(filepath.Join(base, "active_profile"), []byte(active), 0o600)
		if got := hermesHome(home); got != want {
			t.Errorf("active %q: %s, want %s", active, got, want)
		}
	}
	t.Setenv("HERMES_HOME", "/custom")
	if got := hermesHome(home); got != "/custom" {
		t.Errorf("HERMES_HOME: %s", got)
	}
}

func TestHermesMCPServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("birdie:\n  x: 1\nmcp_servers:\n  # birdie:\n  github:\n    birdie:\n  agent-bus:\n    command: x\nother:\n  birdie:\n"), 0o600)
	if got := strings.Join(hermesMCPServers(path), ","); got != "agent-bus" {
		t.Errorf("got %q", got)
	}
	if got := hermesMCPServers(filepath.Join(t.TempDir(), "none")); got != nil {
		t.Errorf("missing file: %v", got)
	}
}

func TestOnboardHermes(t *testing.T) {
	o, calls := fakeHermes(t)
	// Ticked because it's found; asked about waking, then a headless row.
	p := &fakePrompter{toggle: []string{itemHermesHeadless}, lines: []string{"~/hw"}, confirms: []bool{true, true}}
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var keys []string
	for _, it := range p.shown {
		keys = append(keys, it.key)
	}
	if got := strings.Join(keys, " "); got != "hermes hermes-headless grok-bot" {
		t.Errorf("items = %q", got)
	}
	if !strings.Contains(read(t, calls), "allow_gateway_injection true") {
		t.Errorf("waking wasn't applied:\n%s", read(t, calls))
	}
	if !strings.Contains(out, "hermes gateway restart") {
		t.Errorf("next steps:\n%s", out)
	}
	st, _ := store.Open(o.db)
	defer st.Close()
	a, err := st.GetAgent(context.Background(), "hermes-headless")
	if err != nil || a.Config["command"] != "hermes acp" || a.Config["cwd"] != filepath.Join(o.home, "hw") {
		t.Errorf("headless agent = %+v, %v", a.Config, err)
	}

	// Run again: connected, and the wake question defaults to what's set. Unticking disconnects.
	o.stamp = "t2"
	p = &fakePrompter{toggle: []string{"hermes"}, confirms: []bool{true}}
	if out, err := runWizard(t, o, p, noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, ok := connectedAs("hermes", o.home); ok {
		t.Error("unticking should disconnect")
	}
	if !strings.Contains(read(t, calls), "plugins disable birdie") {
		t.Error("the plugin wasn't disabled")
	}
}

// TestHermesPlugin runs the plugin's own tests against a real bus.
func TestHermesPlugin(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 isn't installed")
	}
	path := os.Getenv("PATH")
	o := fakeTools(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(o.db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, name := range []string{"hermes", "peer"} {
		if err := createAgent(ctx, st, bus.Agent{Name: name, Delivery: bus.DeliveryPull, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(st, log)
	b.LoopLimit = -1 // the tests send more than a loop allows
	srv := httptest.NewServer(httpapi.New(b, log))
	defer srv.Close()

	cmd := exec.CommandContext(ctx, python, "-I", filepath.Join("..", "..", "integrations", "hermes", "test_plugin.py"))
	cmd.Env = append(os.Environ(), "PATH="+path, "PYTHONDONTWRITEBYTECODE=1",
		"BIRDIE_TEST_URL="+srv.URL,
		"BIRDIE_TEST_TOKEN_FILE="+filepath.Join(tokenDir(), "hermes"),
		"BIRDIE_TEST_PEER_TOKEN_FILE="+filepath.Join(tokenDir(), "peer"))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}
