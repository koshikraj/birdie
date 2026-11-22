package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"birdie/internal/bus"
	"birdie/internal/store"
)

// fakePrompter answers from a script and records what was asked.
type fakePrompter struct {
	confirms []bool
	lines    []string
	secrets  []string
	toggle   []string // item keys to flip in the checklist
	cancel   bool
	shown    []*wizardItem
	asked    []string // questions, in order
}

func (f *fakePrompter) confirm(q string, _ bool) (bool, error) {
	f.asked = append(f.asked, q)
	if len(f.confirms) == 0 {
		return false, errors.New("unexpected confirm")
	}
	a := f.confirms[0]
	f.confirms = f.confirms[1:]
	return a, nil
}

func (f *fakePrompter) line(q, def string) (string, error) {
	f.asked = append(f.asked, q)
	if len(f.lines) == 0 {
		return "", errors.New("unexpected line: " + q)
	}
	a := f.lines[0]
	f.lines = f.lines[1:]
	if a == "" {
		return def, nil
	}
	return a, nil
}

func (f *fakePrompter) secret(string) (string, error) {
	if len(f.secrets) == 0 {
		return "", errors.New("unexpected secret")
	}
	a := f.secrets[0]
	f.secrets = f.secrets[1:]
	return a, nil
}

func (f *fakePrompter) choose(_ string, items []*wizardItem, _ string) error {
	f.shown = items
	if f.cancel {
		return errCancelled
	}
	for _, key := range f.toggle {
		for _, it := range items {
			if it.key == key {
				it.ticked = !it.ticked
			}
		}
	}
	return nil
}

type fakeService struct {
	ok, installed, active bool
	installs              int
}

func (f *fakeService) name() string              { return "fake" }
func (f *fakeService) available() bool           { return f.ok }
func (f *fakeService) state() (bool, bool)       { return f.installed, f.active }
func (f *fakeService) install(_, _ string) error { f.installs++; return nil }
func (f *fakeService) remove() error             { return nil }

func runWizard(t *testing.T, o setupOptions, p *fakePrompter, svc serviceManager) (string, error) {
	t.Helper()
	var out bytes.Buffer
	w := &wizard{ctx: context.Background(), p: p, out: &out, svc: svc, o: o, url: "http://127.0.0.1:1"}
	err := w.run()
	return out.String(), err
}

func noSvc() *fakeService { return &fakeService{} }

func TestOnboardConnectsOnlyTicked(t *testing.T) {
	o := fakeTools(t, "codex", "grok")
	// Everything found starts ticked; untick Grok CLI, confirm.
	p := &fakePrompter{toggle: []string{"grok"}, confirms: []bool{true}}
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !exists(filepath.Join(o.home, ".codex", "config.toml")) {
		t.Error("codex wasn't connected")
	}
	if exists(filepath.Join(o.home, ".grok")) {
		t.Error("grok was connected after being unticked")
	}
	if !strings.Contains(out, "Changes to make") || !strings.Contains(out, "Health check") {
		t.Errorf("output:\n%s", out)
	}
}

func TestOnboardListsWhatItFinds(t *testing.T) {
	o := fakeTools(t, "codex")
	p := &fakePrompter{cancel: true}
	if _, err := runWizard(t, o, p, noSvc()); !errors.Is(err, errCancelled) {
		t.Fatalf("err = %v", err)
	}
	var keys []string
	for _, it := range p.shown {
		keys = append(keys, it.key)
		if want := it.key == "codex"; it.ticked != want {
			t.Errorf("%s ticked = %v, want %v", it.key, it.ticked, want)
		}
	}
	// A headless row needs its tool on PATH; Grok Bot is always offered.
	if got := strings.Join(keys, " "); got != "codex grok-bot" {
		t.Errorf("items = %q", got)
	}
}

func TestOnboardChangesNothingUnlessConfirmed(t *testing.T) {
	o := fakeTools(t, "codex")
	svc := &fakeService{ok: true}
	p := &fakePrompter{confirms: []bool{true, false}} // service yes, apply no
	if _, err := runWizard(t, o, p, svc); !errors.Is(err, errCancelled) {
		t.Fatalf("err = %v", err)
	}
	if exists(filepath.Join(o.home, ".codex")) || svc.installs != 0 {
		t.Fatal("something changed without confirmation")
	}
}

func TestOnboardGrokBotPastedKey(t *testing.T) {
	o := fakeTools(t)
	const key = "test-key-not-real"
	p := &fakePrompter{
		toggle:   []string{itemBot},
		lines:    []string{"not a url", "https://hooks.example.com/abc/secret-id", ""},
		secrets:  []string{"", key},
		confirms: []bool{true, false},
	}
	// The second blank line is the file-path prompt again after an empty paste.
	p.lines = append(p.lines, "")
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, key) || strings.Contains(out, "secret-id") {
		t.Fatalf("output leaks the key or webhook path:\n%s", out)
	}
	path := defaultBotKeyPath()
	b, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(b)) != key {
		t.Fatalf("key file = %q, %v", b, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v", fi.Mode().Perm())
	}
	if !keyFileOK(routinePath()) {
		t.Error("routine text wasn't saved")
	}
	st, err := store.Open(o.db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := st.GetAgent(context.Background(), botAgent)
	if err != nil {
		t.Fatal(err)
	}
	if a.Delivery != "webhook" || a.Config["auth_file"] != path {
		t.Errorf("agent = %+v", a)
	}
	if !strings.Contains(out, "grok-bot-prompt.md") {
		t.Errorf("doesn't say where the prompt is:\n%s", out)
	}
}

// The webhook comes from Grok Bot, so it can only be asked for after the prompt.
func TestOnboardGrokBotAsksForWebhookAfterPrompt(t *testing.T) {
	o := fakeTools(t)
	p := botWizard(true, true)
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	at := func(q string) int {
		for i, a := range p.asked {
			if strings.Contains(a, q) {
				return i
			}
		}
		t.Fatalf("never asked %q: %q", q, p.asked)
		return -1
	}
	if apply, show, url := at("Apply these changes"), at("Show it here"), at("Webhook URL"); !(apply < show && show < url) {
		t.Errorf("wrong order: %q", p.asked)
	}
	if !strings.Contains(out, "then gives you a prompt") {
		t.Errorf("the preview doesn't mention the prompt:\n%s", out)
	}
	a := botAgentRow(t, o)
	if a.Delivery != "webhook" || a.Config["url"] != "https://hooks.example.com/1" || a.Config["auth_file"] != defaultBotKeyPath() {
		t.Errorf("agent = %+v", a)
	}
}

// Skipping the webhook keeps the agent and its token, and a rerun picks it up.
func TestOnboardGrokBotFinishLater(t *testing.T) {
	o := fakeTools(t)
	p := &fakePrompter{toggle: []string{itemBot}, lines: []string{""}, confirms: []bool{true, false}}
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "run: birdie setup grok-bot") {
		t.Errorf("doesn't say how to finish:\n%s", out)
	}
	if !strings.Contains(out, "waiting for its webhook URL and key") {
		t.Errorf("the health check doesn't mention it:\n%s", out)
	}
	if a := botAgentRow(t, o); a.Delivery != "pull" || a.Config["url"] != "" {
		t.Fatalf("agent = %+v", a)
	}
	token := botToken(t)

	// The rerun has it ticked, doesn't resend the prompt unless asked, and finishes.
	p = &fakePrompter{
		lines:    []string{"https://hooks.example.com/1", ""},
		secrets:  []string{"test-key-not-real"},
		confirms: []bool{false},
	}
	out, err = runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !p.shown[len(p.shown)-1].ticked {
		t.Errorf("not ticked on the rerun:\n%s", out)
	}
	if strings.Contains(out, token) {
		t.Errorf("reprinted the token without being asked:\n%s", out)
	}
	if a := botAgentRow(t, o); a.Delivery != "webhook" {
		t.Errorf("agent = %+v", a)
	}
	if botToken(t) != token {
		t.Error("finishing replaced the token, which the routine already has")
	}

	// Once connected, a rerun asks nothing about the prompt.
	p = &fakePrompter{confirms: []bool{false}}
	if out, err := runWizard(t, o, p, noSvc()); err != nil || !strings.Contains(out, "Nothing to change") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestOnboardGrokBotUntickWhileWaiting(t *testing.T) {
	o := fakeTools(t)
	p := &fakePrompter{toggle: []string{itemBot}, lines: []string{""}, confirms: []bool{true, false}}
	if out, err := runWizard(t, o, p, noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	p = &fakePrompter{toggle: []string{itemBot}, confirms: []bool{true}}
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(filepath.Join(tokenDir(), botAgent)) || exists(botPromptPath()) {
		t.Errorf("left the token or prompt behind:\n%s", out)
	}
	if strings.Contains(out, "Finish Grok Bot") {
		t.Errorf("tried to finish an agent it just removed:\n%s", out)
	}
}

func TestSetupGrokBotInTerminal(t *testing.T) {
	o := fakeTools(t)
	var out bytes.Buffer
	p := &fakePrompter{
		lines:    []string{"https://hooks.example.com/1", ""},
		secrets:  []string{"test-key-not-real"},
		confirms: []bool{true},
	}
	if err := setupBot(context.Background(), o, "http://127.0.0.1:1", "", "", p, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "BIRDIE_TOKEN = "+botToken(t)) {
		t.Errorf("no prompt:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "The bus isn't running") {
		t.Errorf("doesn't warn that the check will fail:\n%s", out.String())
	}
	a := botAgentRow(t, o)
	if a.Delivery != "webhook" || a.Config["url"] != "https://hooks.example.com/1" {
		t.Errorf("agent = %+v", a)
	}
	if !keyFileOK(routinePath()) {
		t.Error("routine text wasn't saved")
	}
}

func TestSetupGrokBotInScript(t *testing.T) {
	o := fakeTools(t)
	ctx := context.Background()
	const url = "http://127.0.0.1:1"
	run := func(webhook, authFile string) string {
		t.Helper()
		var out bytes.Buffer
		if err := setupBot(ctx, o, url, webhook, authFile, nil, &out); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		return out.String()
	}
	out := run("", "")
	token := botToken(t)
	if strings.Contains(out, token) || exists(botPromptPath()) {
		t.Errorf("gave out the token without being asked:\n%s", out)
	}
	for _, want := range []string{"birdie agent prompt grok-bot", "birdie setup grok-bot --webhook"} {
		if !strings.Contains(out, want) {
			t.Errorf("next steps lack %q:\n%s", want, out)
		}
	}
	if a := botAgentRow(t, o); a.Delivery != "pull" {
		t.Errorf("agent = %+v", a)
	}
	run("", "") // running it twice is fine

	keyFile := filepath.Join(o.home, "key")
	os.WriteFile(keyFile, []byte("k\n"), 0o600)
	run("https://hooks.example.com/1", keyFile)
	a := botAgentRow(t, o)
	if a.Delivery != "webhook" || a.Config["url"] != "https://hooks.example.com/1" || a.Config["auth_file"] != keyFile {
		t.Errorf("agent = %+v", a)
	}
	if botToken(t) != token {
		t.Error("connecting replaced the token, which the routine already has")
	}
	if out := run("", ""); !strings.Contains(out, "already set up") {
		t.Errorf("rerun:\n%s", out)
	}

	// It can change the webhook, or do everything at once on a fresh machine.
	run("https://hooks.example.com/2", keyFile)
	if a := botAgentRow(t, o); a.Config["url"] != "https://hooks.example.com/2" {
		t.Errorf("agent = %+v", a)
	}
	if botToken(t) != token {
		t.Error("changing the webhook replaced the token")
	}
}

func TestSetupGrokBotDirectlyWithWebhook(t *testing.T) {
	o := fakeTools(t)
	keyFile := filepath.Join(o.home, "key")
	os.WriteFile(keyFile, []byte("k\n"), 0o600)
	var out bytes.Buffer
	if err := setupBot(context.Background(), o, "http://127.0.0.1:1", "https://hooks.example.com/1", keyFile, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if a := botAgentRow(t, o); a.Delivery != "webhook" {
		t.Errorf("agent = %+v", a)
	}
	if !strings.Contains(out.String(), "birdie agent prompt grok-bot") {
		t.Errorf("doesn't say how to get the prompt:\n%s", out.String())
	}
}

func TestSetupGrokBotRefusals(t *testing.T) {
	o := fakeTools(t)
	ctx := context.Background()
	keyFile := filepath.Join(o.home, "key")
	os.WriteFile(keyFile, []byte("k\n"), 0o600)
	for _, c := range [][2]string{
		{"not a url", keyFile},
		{"https://hooks.example.com/1", ""},
		{"", keyFile},
		{"https://hooks.example.com/1", filepath.Join(o.home, "missing")},
	} {
		if err := setupBot(ctx, o, "http://127.0.0.1:1", c[0], c[1], nil, &bytes.Buffer{}); err == nil {
			t.Errorf("setupBot(%q, %q) should fail", c[0], c[1])
		}
	}
	if exists(filepath.Join(tokenDir(), botAgent)) {
		t.Error("a refused setup still registered the agent")
	}
	for _, args := range [][]string{
		{botAgent, "--as", "other", "--db", o.db},
		{"codex", "--webhook", "https://hooks.example.com/1", "--db", o.db},
		{"--all", "--auth-file", keyFile, "--db", o.db},
	} {
		if err := setup(ctx, args); err == nil {
			t.Errorf("setup %v should fail", args)
		}
	}
}

func TestSetupGrokBotDryRunAndRemove(t *testing.T) {
	o := fakeTools(t)
	ctx := context.Background()
	const url = "http://127.0.0.1:1"
	dry := o
	dry.dry = true
	var out bytes.Buffer
	if err := setupBot(ctx, dry, url, "", "", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would add agent grok-bot") || exists(filepath.Join(tokenDir(), botAgent)) || exists(routinePath()) {
		t.Errorf("dry run:\n%s", out.String())
	}

	if err := setupBot(ctx, o, url, "", "", nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := saveBotPrompt("prompt"); err != nil {
		t.Fatal(err)
	}
	remove := o
	remove.remove = true
	dryRemove := remove
	dryRemove.dry = true
	if err := setupBot(ctx, dryRemove, url, "", "", nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(tokenDir(), botAgent)) {
		t.Error("the dry run removed the token")
	}
	if err := setupBot(ctx, remove, url, "", "", nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(tokenDir(), botAgent)) || exists(botPromptPath()) {
		t.Error("left the token or prompt behind")
	}
	out.Reset()
	if err := setupBot(ctx, remove, url, "", "", nil, &out); err != nil || !strings.Contains(out.String(), "nothing to remove") {
		t.Errorf("%v\n%s", err, out.String())
	}
}

func botAgentRow(t *testing.T, o setupOptions) bus.Agent {
	t.Helper()
	st, err := store.Open(o.db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := st.GetAgent(context.Background(), botAgent)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// botToken reads the token onboarding generated for Grok Bot.
func botToken(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(tokenDir(), botAgent))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func botWizard(confirms ...bool) *fakePrompter {
	return &fakePrompter{
		toggle:   []string{itemBot},
		lines:    []string{"https://hooks.example.com/1", ""},
		secrets:  []string{"test-key-not-real"},
		confirms: confirms,
	}
}

func TestOnboardGrokBotPromptInTerminal(t *testing.T) {
	o := fakeTools(t)
	out, err := runWizard(t, o, botWizard(true, true), noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"BIRDIE_URL = http://127.0.0.1:1",
		"BIRDIE_TOKEN = " + botToken(t),
		"POST $BIRDIE_URL/v1/messages", // from the routine
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"```", "# Grok Bot routine", "test-key-not-real"} {
		if strings.Contains(out, not) {
			t.Errorf("output shouldn't have %q:\n%s", not, out)
		}
	}
	if exists(botPromptPath()) {
		t.Error("showing the prompt shouldn't also save it")
	}
}

func TestOnboardGrokBotPromptInFile(t *testing.T) {
	o := fakeTools(t)
	out, err := runWizard(t, o, botWizard(true, false), noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	token := botToken(t)
	if strings.Contains(out, token) {
		t.Fatalf("saving the prompt shouldn't print the token:\n%s", out)
	}
	b, err := os.ReadFile(botPromptPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "BIRDIE_TOKEN = "+token) || !strings.Contains(string(b), "POST $BIRDIE_URL/v1/messages") {
		t.Errorf("prompt file:\n%s", b)
	}
	if fi, _ := os.Stat(botPromptPath()); fi.Mode().Perm() != 0o600 {
		t.Errorf("prompt file mode = %v", fi.Mode().Perm())
	}

	// A loose copy from before is replaced with a private one.
	if err := os.Chmod(botPromptPath(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := saveBotPrompt("new"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(botPromptPath()); fi.Mode().Perm() != 0o600 {
		t.Errorf("resaved prompt file mode = %v", fi.Mode().Perm())
	}

	// Disconnecting removes the file, since its token no longer works.
	p := &fakePrompter{toggle: []string{itemBot}, confirms: []bool{true}}
	if out, err := runWizard(t, o, p, noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(botPromptPath()) {
		t.Error("the prompt file outlived the agent")
	}
}

func TestOnboardGrokBotPromptQuestionCancelled(t *testing.T) {
	o := fakeTools(t)
	// The changes are applied before this question, so backing out of it isn't an error.
	out, err := runWizard(t, o, botWizard(true), noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "birdie agent prompt grok-bot") {
		t.Errorf("doesn't say how to get the prompt later:\n%s", out)
	}
	if strings.Contains(out, botToken(t)) || exists(botPromptPath()) {
		t.Errorf("gave out the prompt anyway:\n%s", out)
	}
}

func TestAgentPromptCommand(t *testing.T) {
	o := fakeTools(t)
	ctx := context.Background()
	if err := agentCmd(ctx, []string{"prompt", botAgent, "--db", o.db}); err == nil || !strings.Contains(err.Error(), "birdie setup grok-bot") {
		t.Errorf("before connecting: %v", err)
	}
	if out, err := runWizard(t, o, botWizard(true, true), noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := agentCmd(ctx, []string{"prompt", "codex", "--db", o.db}); err == nil {
		t.Error("only Grok Bot has a prompt")
	}
	if err := agentCmd(ctx, []string{"prompt", botAgent, "--save", "--url", "http://bus.test", "--db", o.db}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(botPromptPath())
	if !strings.Contains(string(b), "BIRDIE_URL = http://bus.test") {
		t.Errorf("prompt file:\n%s", b)
	}
	if err := agentCmd(ctx, []string{"rm", botAgent, "--db", o.db}); err != nil {
		t.Fatal(err)
	}
	if exists(botPromptPath()) {
		t.Error("the prompt file outlived the agent")
	}
}

func TestOnboardGrokBotKeyFromFileAndRerun(t *testing.T) {
	o := fakeTools(t)
	keyFile := filepath.Join(o.home, "my-key")
	os.WriteFile(keyFile, []byte("k\n"), 0o600)
	p := &fakePrompter{
		toggle:   []string{itemBot},
		lines:    []string{"https://hooks.example.com/1", filepath.Join(o.home, "missing"), keyFile},
		confirms: []bool{true, false},
	}
	if out, err := runWizard(t, o, p, noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(defaultBotKeyPath()) {
		t.Error("a key from a file shouldn't be copied")
	}
	token := filepath.Join(tokenDir(), botAgent)
	first, _ := os.ReadFile(token)

	// Running again: ticked, no changes asked for, and the token survives.
	p = &fakePrompter{confirms: []bool{false}}
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !p.shown[len(p.shown)-1].ticked || !strings.Contains(out, "Nothing to change") {
		t.Fatalf("rerun:\n%s", out)
	}

	// Changing the URL keeps the token.
	p = &fakePrompter{
		confirms: []bool{true, true},
		lines:    []string{"https://new.example.com/2", keyFile},
	}
	if out, err := runWizard(t, o, p, noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	st, _ := store.Open(o.db)
	a, _ := st.GetAgent(context.Background(), botAgent)
	st.Close()
	if a.Config["url"] != "https://new.example.com/2" {
		t.Errorf("url = %q", a.Config["url"])
	}
	if second, _ := os.ReadFile(token); string(second) != string(first) {
		t.Error("changing the URL replaced the token")
	}

	// Unticking deletes the agent and token, but not the key file.
	p = &fakePrompter{toggle: []string{itemBot}, confirms: []bool{true}}
	out, err = runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "would delete agent grok-bot") {
		t.Errorf("preview doesn't warn:\n%s", out)
	}
	if exists(token) || !exists(keyFile) {
		t.Errorf("token still there = %v, key file kept = %v", exists(token), exists(keyFile))
	}
}

func TestOnboardUntickDisconnectsAndKeepsAgentName(t *testing.T) {
	o := fakeTools(t, "codex")
	codexOpts := o
	codexOpts.as = "my-codex"
	if err := setupOne(context.Background(), "codex", codexOpts, false); err != nil {
		t.Fatal(err)
	}
	p := &fakePrompter{cancel: true}
	runWizard(t, o, p, noSvc())
	if it := p.shown[0]; it.agent != "my-codex" || !it.connected || !it.ticked {
		t.Fatalf("item = %+v", it)
	}

	o.stamp = "t2"
	p = &fakePrompter{toggle: []string{"codex"}, confirms: []bool{true}}
	if out, err := runWizard(t, o, p, noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(o.home, ".codex", "config.toml"))
	if strings.Contains(string(b), "birdie") {
		t.Fatalf("still connected: %s", b)
	}
	if !exists(filepath.Join(tokenDir(), "my-codex")) {
		t.Error("disconnecting a tool should keep its token")
	}
}

func TestOnboardHeadlessGrok(t *testing.T) {
	o := fakeTools(t, "grok")
	p := &fakePrompter{toggle: []string{"grok", itemHeadless}, lines: []string{"~/work"}, confirms: []bool{true}}
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	st, _ := store.Open(o.db)
	defer st.Close()
	a, err := st.GetAgent(context.Background(), "grok")
	if err != nil {
		t.Fatal(err)
	}
	if a.Delivery != "acp" || a.Config["cwd"] != filepath.Join(o.home, "work") || a.Config["permission"] != "reject" {
		t.Errorf("agent config = %+v", a.Config)
	}
}

func TestOnboardAntigravity(t *testing.T) {
	o := fakeTools(t, "agy")
	p := &fakePrompter{toggle: []string{itemAgyHeadless}, lines: []string{"~/agy"}, confirms: []bool{true}}
	out, err := runWizard(t, o, p, noSvc())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var keys []string
	for _, it := range p.shown {
		keys = append(keys, it.key)
	}
	if !strings.Contains(strings.Join(keys, " "), "antigravity "+itemAgyHeadless) {
		t.Fatalf("items = %v", keys)
	}
	if a, ok := connectedAs("antigravity", o.home); !ok || a != "antigravity" {
		t.Fatalf("connectedAs = %q, %v\n%s", a, ok, out)
	}
	st, _ := store.Open(o.db)
	defer st.Close()
	a, err := st.GetAgent(context.Background(), "antigravity-headless")
	if err != nil {
		t.Fatal(err)
	}
	if a.Delivery != "acp" || a.Config["command"] != "agy acp" || a.Config["cwd"] != filepath.Join(o.home, "agy") || a.Config["permission"] != "reject" {
		t.Errorf("agent config = %+v", a.Config)
	}
	if strings.Contains(a.Config["command"], "dangerously") {
		t.Error("headless Antigravity must not skip permissions")
	}

	// Unticking the headless row deletes its agent.
	o.stamp = "t2"
	p = &fakePrompter{toggle: []string{itemAgyHeadless}, confirms: []bool{true}}
	if out, err := runWizard(t, o, p, noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := st.GetAgent(context.Background(), "antigravity-headless"); err == nil {
		t.Error("headless agent still there")
	}
}

func TestOnboardService(t *testing.T) {
	for name, c := range map[string]struct {
		svc      fakeService
		confirms []bool
		installs int
	}{
		"accepted":    {fakeService{ok: true}, []bool{true, true}, 1},
		"declined":    {fakeService{ok: true}, []bool{false, true}, 0},
		"unavailable": {fakeService{}, []bool{true}, 0},
		"refreshed":   {fakeService{ok: true, installed: true, active: true}, []bool{true}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			o := fakeTools(t, "codex")
			svc := c.svc
			p := &fakePrompter{confirms: c.confirms}
			out, _ := runWizard(t, o, p, &svc) // the bus isn't really up, so a service step can report failure
			if svc.installs != c.installs {
				t.Fatalf("installs = %d, want %d\n%s", svc.installs, c.installs, out)
			}
		})
	}
}

func TestConnectedAs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, env := range []string{"GROK_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(env, "")
	}
	if _, ok := connectedAs("claude", home); ok {
		t.Error("claude connected with no config")
	}
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"birdie":{"args":["mcp","--as","me","--channel"]}},"other":1}`), 0o600)
	if a, ok := connectedAs("claude", home); !ok || a != "me" {
		t.Errorf("claude = %q, %v", a, ok)
	}
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	cfg := filepath.Join(home, ".codex", "config.toml")
	os.WriteFile(cfg, []byte("[mcp_servers.other]\nargs = [\"--as\", \"wrong\"]\n"), 0o600)
	if _, ok := connectedAs("codex", home); ok {
		t.Error("another server counted as birdie")
	}
	os.WriteFile(cfg, []byte("[mcp_servers.other]\nargs = [\"--as\", \"wrong\"]\n\n[mcp_servers.birdie]\ncommand = \"x\"\nargs = [\"mcp\", \"--as\", \"mine\"]\n"), 0o600)
	if a, ok := connectedAs("codex", home); !ok || a != "mine" {
		t.Errorf("codex = %q, %v", a, ok)
	}
}

func TestChecklistKeys(t *testing.T) {
	a, b := &wizardItem{label: "A", ticked: true}, &wizardItem{label: "B"}
	c := &checklist{items: []*wizardItem{a, b}}
	c.key("\x1b[A") // up wraps to the last item
	if c.cursor != 1 {
		t.Fatalf("cursor = %d", c.cursor)
	}
	c.key(" ")
	if !b.ticked || !a.ticked {
		t.Fatalf("toggle: %v %v", a.ticked, b.ticked)
	}
	c.key("\x1b[B")
	c.key(" ")
	if a.ticked {
		t.Fatal("untick failed")
	}
	if c.key("\r") != keyDone || c.key("q") != keyCancel || c.key("\x03") != keyCancel {
		t.Fatal("done or cancel keys")
	}
	lines := c.render("t", "", 10)
	for _, l := range lines {
		if len([]rune(l)) >= 10 {
			t.Errorf("line can wrap: %q", l)
		}
	}
}

func TestSplitKeys(t *testing.T) {
	got := splitKeys("\x1b[B\x1b[B \x1bOA\r")
	want := []string{"\x1b[B", "\x1b[B", " ", "\x1bOA", "\r"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestDoctorNeverPrintsSecrets(t *testing.T) {
	o := fakeTools(t)
	keyFile := filepath.Join(o.home, "key")
	os.WriteFile(keyFile, []byte("key-value-xyz\n"), 0o600)
	p := &fakePrompter{toggle: []string{itemBot}, lines: []string{"https://hooks.example.com/hidden-path", keyFile}, confirms: []bool{true, false}}
	if out, err := runWizard(t, o, p, noSvc()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	token, _ := readToken(botAgent)
	var b bytes.Buffer
	printChecks(&b, runChecks(context.Background(), o.db, "http://127.0.0.1:1", o.home, noSvc()))
	got := b.String()
	if token == "" || strings.Contains(got, token) || strings.Contains(got, "key-value-xyz") || strings.Contains(got, "hidden-path") {
		t.Fatalf("doctor leaks a secret:\n%s", got)
	}
	if !strings.Contains(got, "hooks.example.com") || !strings.Contains(got, "✗ bus isn't reachable") {
		t.Errorf("doctor output:\n%s", got)
	}
	os.Remove(keyFile)
	b.Reset()
	printChecks(&b, runChecks(context.Background(), o.db, "http://127.0.0.1:1", o.home, noSvc()))
	if !strings.Contains(b.String(), "can't read its key file") {
		t.Errorf("missing key file not reported:\n%s", b.String())
	}
}

func TestServiceFiles(t *testing.T) {
	unit := systemdUnitText("/opt/a b/birdie", "/data/100%/bus.db", "/x:/y")
	for _, want := range []string{`ExecStart="/opt/a b/birdie" serve --db "/data/100%%/bus.db"`, `Environment="PATH=/x:/y"`, "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	plist := launchdPlist("/a&b/birdie", "/db", "/x:/y", "/log")
	if !strings.Contains(plist, "/a&amp;b/birdie") || strings.Contains(plist, "/a&b/") {
		t.Errorf("plist isn't escaped:\n%s", plist)
	}
	if !strings.HasPrefix(servicePath("/inst/birdie"), "/inst"+string(os.PathListSeparator)) {
		t.Error("service PATH lacks the program's folder")
	}
}
