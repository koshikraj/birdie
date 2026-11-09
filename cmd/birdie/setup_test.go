package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

func TestSetTOMLBlock(t *testing.T) {
	text := "model = \"x\"\n\n[mcp_servers.other]\ncommand = \"o\"\n\n[mcp_servers.birdie]\ncommand = \"old\"\n\n[mcp_servers.birdie.env]\nA = \"1\"\n\n[tui]\ntheme = \"dark\"\n"
	block := "[mcp_servers.birdie]\ncommand = \"new\"\n"

	got := setTOMLBlock(text, "mcp_servers.birdie", block)
	if strings.Contains(got, "old") || strings.Contains(got, "A = ") || !strings.Contains(got, `command = "new"`) {
		t.Fatalf("replace:\n%s", got)
	}
	for _, keep := range []string{`model = "x"`, "[mcp_servers.other]", "[tui]", `theme = "dark"`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("lost %q:\n%s", keep, got)
		}
	}
	if again := setTOMLBlock(got, "mcp_servers.birdie", block); again != got {
		t.Fatalf("second run changed the file:\n%s\n---\n%s", got, again)
	}
	removed := setTOMLBlock(got, "mcp_servers.birdie", "")
	if strings.Contains(removed, "birdie") || !strings.Contains(removed, "[tui]") {
		t.Fatalf("remove:\n%s", removed)
	}
	if empty := setTOMLBlock("", "mcp_servers.birdie", block); empty != block {
		t.Fatalf("empty file: %q", empty)
	}
}

func TestSetupGrokAndCodexRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GROK_HOME", "")
	t.Setenv("CODEX_HOME", "")
	original := "model = \"x\"\n"
	for _, dir := range []string{".grok", ".codex"} {
		os.MkdirAll(filepath.Join(home, dir), 0o700)
		os.WriteFile(filepath.Join(home, dir, "config.toml"), []byte(original), 0o600)
	}
	r := &setupRun{exe: "/bin/agent bus", agent: "me", stamp: "t", home: home}
	if err := setupGrok(r); err != nil {
		t.Fatal(err)
	}
	if err := setupCodex(r); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(home, ".grok", "config.toml"))
	if !strings.Contains(string(cfg), `args = ["mcp", "--as", "me"]`) {
		t.Fatalf("grok config:\n%s", cfg)
	}
	hooks, err := os.ReadFile(filepath.Join(home, ".grok", "hooks", "birdie.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]map[string][]struct {
		Hooks []struct{ Command string }
	}
	if err := json.Unmarshal(hooks, &v); err != nil {
		t.Fatal(err)
	}
	if got := v["hooks"]["Stop"][0].Hooks[0].Command; got != "'/bin/agent bus' hook grok stop --as 'me'" {
		t.Fatalf("stop command = %q", got)
	}
	for _, dir := range []string{".grok", ".codex"} {
		if _, err := os.Stat(filepath.Join(home, dir, "skills", "talk-to-agents", "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
	backup := filepath.Join(home, ".config", "birdie", "backups", "t", ".grok_config.toml")
	if b, err := os.ReadFile(backup); err != nil || string(b) != original {
		t.Fatalf("backup = %q, %v", b, err)
	}

	r.remove, r.stamp = true, "t2"
	if err := setupGrok(r); err != nil {
		t.Fatal(err)
	}
	if err := setupCodex(r); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".grok", ".codex"} {
		if b, _ := os.ReadFile(filepath.Join(home, dir, "config.toml")); string(b) != original {
			t.Fatalf("%s config after remove = %q", dir, b)
		}
		if _, err := os.Stat(filepath.Join(home, dir, "skills", "talk-to-agents")); !os.IsNotExist(err) {
			t.Fatalf("skill left behind in %s", dir)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".grok", "hooks", "birdie.json")); !os.IsNotExist(err) {
		t.Fatal("hooks left behind")
	}
}

func TestGrokHookInput(t *testing.T) {
	sub := "general"
	for _, c := range []struct {
		in      grokHookInput
		event   string
		ignored bool
	}{
		{grokHookInput{SessionID: "s", Reason: "end_turn"}, "stop", false},
		{grokHookInput{SessionID: "s", Reason: "shutdown"}, "stop", true},
		{grokHookInput{SessionID: "s"}, "post-tool-use", false},
		{grokHookInput{SessionID: "s", SubagentType: &sub}, "post-tool-use", true},
		{grokHookInput{}, "post-tool-use", true},
	} {
		if got := c.in.ignored(c.event); got != c.ignored {
			t.Errorf("%+v %s: ignored = %v", c.in, c.event, got)
		}
	}
	if out, err := grokHookOutput("stop", "x"); err != nil || out != `{"decision":"block","reason":"x"}` {
		t.Errorf("stop output = %q, %v", out, err)
	}
	if out, err := grokHookOutput("post-tool-use", "x"); err != nil || !strings.Contains(out, `"hookEventName":"PostToolUse"`) {
		t.Errorf("tool output = %q, %v", out, err)
	}
}

// fakeTools makes a temporary home whose PATH holds only the named commands.
func fakeTools(t *testing.T, names ...string) setupOptions {
	t.Helper()
	home := t.TempDir()
	bin := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, env := range []string{"GROK_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(env, "")
	}
	return setupOptions{db: filepath.Join(home, "bus.db"), exe: "/bin/birdie", home: home, stamp: "t"}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestSetupAll(t *testing.T) {
	o := fakeTools(t, "codex", "grok")
	var out bytes.Buffer
	if err := setupAll(context.Background(), o, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Not found on PATH: claude") {
		t.Errorf("output: %s", out.String())
	}
	for _, f := range []string{".codex/config.toml", ".grok/config.toml", ".config/birdie/tokens/codex", ".config/birdie/tokens/grok-tui"} {
		if !exists(filepath.Join(o.home, f)) {
			t.Errorf("%s is missing", f)
		}
	}
}

func TestSetupAllKeepsGoingAfterAFailure(t *testing.T) {
	o := fakeTools(t, "codex", "grok")
	// A folder where Codex's config should be makes only Codex fail.
	if err := os.MkdirAll(filepath.Join(o.home, ".codex", "config.toml"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := setupAll(context.Background(), o, &out)
	if err == nil || !strings.Contains(err.Error(), "codex") || strings.Contains(err.Error(), "grok") {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if !exists(filepath.Join(o.home, ".grok", "config.toml")) {
		t.Error("grok wasn't set up after codex failed")
	}
}

func TestSetupAllRefusals(t *testing.T) {
	o := fakeTools(t)
	if err := setupAll(context.Background(), o, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "found none") {
		t.Fatalf("no tools: %v", err)
	}
	o = fakeTools(t, "codex")
	o.as = "me"
	if err := setupAll(context.Background(), o, &bytes.Buffer{}); err == nil {
		t.Fatal("--as with --all should be refused")
	}
	o.as, o.remove = "", true
	if err := setupAll(context.Background(), o, &bytes.Buffer{}); err == nil {
		t.Fatal("--remove with --all should be refused")
	}
}

func TestSetupUsage(t *testing.T) {
	fakeTools(t, "codex")
	for _, args := range [][]string{{}, {"codex", "--all"}, {"codex", "grok"}} {
		if err := setup(context.Background(), args); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("setup %v: %v", args, err)
		}
	}
}

func TestSetupReplacesOldName(t *testing.T) {
	o := fakeTools(t, "codex", "grok")
	old := "model = \"x\"\n\n[mcp_servers.agent-bus]\ncommand = \"/old/agent-bus\"\nargs = [\"mcp\", \"--as\", \"mine\"]\n"
	for _, dir := range []string{".codex", ".grok/hooks"} {
		os.MkdirAll(filepath.Join(o.home, dir), 0o700)
	}
	cfg := filepath.Join(o.home, ".codex", "config.toml")
	os.WriteFile(cfg, []byte(old), 0o600)
	grokCfg := filepath.Join(o.home, ".grok", "config.toml")
	os.WriteFile(grokCfg, []byte(old), 0o600)
	hooks := filepath.Join(o.home, ".grok", "hooks", "agent-bus.json")
	os.WriteFile(hooks, []byte("{}"), 0o600)

	// The old entry counts as connected, under its agent name.
	if a, ok := connectedAs("codex", o.home); !ok || a != "mine" {
		t.Fatalf("connectedAs = %q, %v", a, ok)
	}
	if !usesOldName("codex", o.home) {
		t.Fatal("old entry not flagged")
	}

	for _, tool := range []string{"codex", "grok"} {
		if err := setupOne(context.Background(), tool, o, false); err != nil {
			t.Fatal(err)
		}
	}
	if a, ok := connectedAs("codex", o.home); !ok || a != "mine" {
		t.Fatalf("setup changed the agent name: %q, %v", a, ok)
	}
	for _, path := range []string{cfg, grokCfg} {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), "agent-bus") || !strings.Contains(string(b), "[mcp_servers.birdie]") || !strings.Contains(string(b), `model = "x"`) {
			t.Fatalf("%s:\n%s", path, b)
		}
	}
	if exists(hooks) || !exists(filepath.Join(o.home, ".grok", "hooks", "birdie.json")) {
		t.Fatal("grok hooks weren't replaced")
	}
	if usesOldName("codex", o.home) {
		t.Fatal("still flagged after setup")
	}
}

func TestMigrateConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	old, dir := filepath.Join(base, "agent-bus"), filepath.Join(base, "birdie")

	migrateConfig() // nothing to move
	if exists(dir) {
		t.Fatal("created a folder from nothing")
	}

	os.MkdirAll(filepath.Join(old, "tokens"), 0o700)
	os.WriteFile(filepath.Join(old, "tokens", "a"), []byte("t"), 0o600)
	migrateConfig()
	if b, _ := os.ReadFile(filepath.Join(dir, "tokens", "a")); string(b) != "t" {
		t.Fatal("token didn't move")
	}
	if b, _ := os.ReadFile(filepath.Join(old, "tokens", "a")); string(b) != "t" {
		t.Fatal("the old path no longer works")
	}
	if fi, _ := os.Lstat(old); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("old path isn't a link")
	}

	migrateConfig() // running again changes nothing
	if fi, _ := os.Lstat(dir); !fi.IsDir() {
		t.Fatal("second run broke the folder")
	}

	// A new folder that already exists is never overwritten.
	base = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	os.MkdirAll(filepath.Join(base, "agent-bus"), 0o700)
	os.MkdirAll(filepath.Join(base, "birdie"), 0o700)
	migrateConfig()
	if fi, _ := os.Lstat(filepath.Join(base, "agent-bus")); !fi.IsDir() {
		t.Fatal("old folder was touched though the new one exists")
	}
}

func TestEnsureAgentSavesTheLabel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ctx := context.Background()
	db := filepath.Join(home, "bus.db")

	// A new agent gets its tool's label, whatever it's called.
	if err := ensureAgent(ctx, db, "reviewer", "Claude Code", "a reviewer"); err != nil {
		t.Fatal(err)
	}
	// One added before birdie saved labels gets it on the next setup.
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := createAgent(ctx, st, bus.Agent{Name: "old", Delivery: bus.DeliveryPull, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := ensureAgent(ctx, db, "old", "Codex", "x"); err != nil {
		t.Fatal(err)
	}

	st, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for name, want := range map[string]string{"reviewer": "Claude Code", "old": "Codex"} {
		a, err := st.GetAgent(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if a.Label != want {
			t.Errorf("%s: label %q, want %q", name, a.Label, want)
		}
	}
}
