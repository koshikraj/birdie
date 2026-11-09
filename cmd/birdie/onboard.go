package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"birdie"
	"birdie/internal/bus"
	"birdie/internal/store"
)

const (
	itemHeadless       = "grok-headless"
	itemHermesHeadless = "hermes-headless"
	itemBot            = "grok-bot"

	botAgent = "grok-bot"
)

// headlessAgent is an agent birdie starts for each message, over ACP.
type headlessAgent struct {
	key, label, tool, agent, command, folder, description string
}

var headlessAgents = []headlessAgent{
	{itemHeadless, "Grok CLI (headless)", "grok", "grok", "grok agent stdio", "grok-work", "Grok CLI: coding questions and quick research"},
	{itemHermesHeadless, "Hermes (headless)", "hermes", "hermes-headless", "hermes acp", "hermes-work", "Hermes Agent: answers with nothing open"},
}

func headlessFor(key string) (headlessAgent, bool) {
	for _, h := range headlessAgents {
		if h.key == key {
			return h, true
		}
	}
	return headlessAgent{}, false
}

// wizardItem is one row of the onboarding checklist.
type wizardItem struct {
	key, label, note string
	agent            string
	ticked           bool
	connected        bool
	waiting          bool // Grok Bot is registered, but has no webhook yet
}

// botDetails are the answers for connecting or changing Grok Bot.
type botDetails struct {
	url       string
	keyFile   string
	pastedKey string // saved to keyFile when set
}

type wizardPlan struct {
	service             bool
	connect, disconnect []*wizardItem
	bot                 *botDetails
	headlessCwd         map[string]string
	hermesWake          *bool
}

func (p wizardPlan) empty() bool {
	return !p.service && len(p.connect) == 0 && len(p.disconnect) == 0
}

type wizard struct {
	ctx context.Context
	p   prompter
	out io.Writer
	svc serviceManager
	o   setupOptions
	url string
}

func onboard(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("onboard", flag.ContinueOnError)
	db := fs.String("db", defaultDB(), "SQLite database path")
	url := fs.String("url", envOr("BIRDIE_URL", defaultURL), "birdie base URL")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	o, err := newSetupOptions(*db)
	if err != nil {
		return err
	}
	t, closeTerminal, err := openTerminal(ctx, os.Stdout)
	if err != nil {
		return errors.New("onboard asks questions, so it needs a terminal; in a script, use: birdie setup --all")
	}
	defer closeTerminal()
	w := &wizard{ctx: ctx, p: t, out: os.Stdout, svc: newServiceManager(), o: o, url: *url}
	err = w.run()
	if errors.Is(err, errCancelled) {
		fmt.Fprintln(w.out, "Cancelled, so nothing changed.")
		return nil
	}
	return err
}

func (w *wizard) run() error {
	fmt.Fprintln(w.out, "Let's connect your agents to birdie. Nothing changes until you confirm.")
	fmt.Fprintln(w.out)

	plan := wizardPlan{headlessCwd: map[string]string{}}
	var err error
	if plan.service, err = w.askService(); err != nil {
		return err
	}
	items, missing, err := w.items()
	if err != nil {
		return err
	}
	footer := ""
	if len(missing) > 0 {
		footer = "Not found on PATH: " + strings.Join(missing, ", ")
	}
	fmt.Fprintln(w.out)
	if err := w.p.choose("What should connect?", items, footer); err != nil {
		return err
	}
	for _, it := range items {
		switch {
		case it.ticked && !it.connected:
			plan.connect = append(plan.connect, it)
		case it.ticked && setupTools[it.key].apply != nil:
			plan.connect = append(plan.connect, it) // refresh the config
		case !it.ticked && it.connected:
			plan.disconnect = append(plan.disconnect, it)
		}
	}
	if err := w.askDetails(&plan, items); err != nil {
		return err
	}
	if plan.empty() {
		fmt.Fprintln(w.out, "\nNothing to change.")
	} else {
		w.preview(plan)
		ok, err := w.p.confirm("\nApply these changes?", false)
		if err != nil {
			return err
		}
		if !ok {
			return errCancelled
		}
	}
	failed := w.apply(plan)
	for _, it := range items {
		if it.key == itemBot && it.ticked && w.botWaiting() {
			if err := w.finishBot(it.waiting); err != nil {
				fmt.Fprintf(w.out, "failed: %v\n", err)
				failed = append(failed, it.label)
			}
		}
	}
	if w.ctx.Err() != nil {
		return nil // interrupted after the changes were applied, so they stand
	}

	fmt.Fprintln(w.out, "\nHealth check:")
	printChecks(w.out, runChecks(w.ctx, w.o.db, w.url, w.o.home, w.svc))
	w.nextSteps(plan)
	if len(failed) > 0 {
		return fmt.Errorf("these steps failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// askService decides whether to install, or refresh, the background service.
func (w *wizard) askService() (bool, error) {
	if !w.svc.available() {
		if !busUp(w.ctx, w.url) {
			fmt.Fprintln(w.out, "This system has no background service birdie can use, so keep `birdie serve` running yourself.")
		}
		return false, nil
	}
	installed, active := w.svc.state()
	if installed && active {
		return true, nil // restarted so it runs this version
	}
	if busUp(w.ctx, w.url) {
		fmt.Fprintf(w.out, "A bus is already running at %s outside the service, so I'll leave it alone.\n", w.url)
		return false, nil
	}
	return w.p.confirm("Keep birdie running in the background, starting at login?", true)
}

// items lists what can be connected and what's connected already.
func (w *wizard) items() ([]*wizardItem, []string, error) {
	found, _ := detectTools()
	onPath := map[string]bool{}
	for _, name := range found {
		onPath[name] = true
	}
	var items []*wizardItem
	var notFound []string
	for _, name := range setupOrder {
		tool := setupTools[name]
		agent, connected := connectedAs(name, w.o.home)
		if !onPath[name] && !connected {
			notFound = append(notFound, tool.label)
			continue
		}
		it := &wizardItem{key: name, label: tool.label, agent: tool.agent, ticked: true, connected: connected, note: "found"}
		if connected {
			it.agent, it.note = agent, "connected as "+agent
		}
		items = append(items, it)
	}

	st, err := store.Open(w.o.db)
	if err != nil {
		return nil, nil, err
	}
	defer st.Close()
	has := func(name, delivery string) bool {
		a, err := st.GetAgent(w.ctx, name)
		return err == nil && a.Delivery == delivery
	}
	for _, h := range headlessAgents {
		if connected := has(h.agent, "acp"); onPath[h.tool] || connected {
			it := &wizardItem{key: h.key, label: h.label, agent: h.agent, ticked: connected, connected: connected, note: "answers with nothing open"}
			if connected {
				it.note = "connected as " + h.agent
			}
			items = append(items, it)
		}
	}
	connected := has(botAgent, "webhook")
	waiting := has(botAgent, bus.DeliveryPull)
	it := &wizardItem{key: itemBot, label: "Grok Bot", agent: botAgent, ticked: connected || waiting, connected: connected || waiting, waiting: waiting, note: "gives you a prompt, then asks for its webhook"}
	switch {
	case connected:
		it.note = "connected as " + botAgent
	case waiting:
		it.note = "waiting for its webhook URL and key"
	}
	items = append(items, it)
	return items, notFound, nil
}

// askDetails collects what the ticked items still need.
func (w *wizard) askDetails(plan *wizardPlan, items []*wizardItem) error {
	for _, it := range items {
		if !it.ticked {
			continue
		}
		if h, ok := headlessFor(it.key); ok {
			if it.connected {
				continue
			}
			fmt.Fprintf(w.out, "\n%s\n", h.label)
			dir, err := w.p.line("Folder it works in", filepath.Join(w.o.home, h.folder))
			if err != nil {
				return err
			}
			plan.headlessCwd[h.key] = expandHome(dir, w.o.home)
			continue
		}
		switch it.key {
		case "hermes":
			current, _ := readHermesSettings(hermesHome(w.o.home))
			fmt.Fprintln(w.out, "\nHermes")
			wake, err := w.p.confirm("Let other agents' messages start turns in Hermes on their own? That uses your model account", current.Wake)
			if err != nil {
				return err
			}
			plan.hermesWake = &wake
		case itemBot:
			if !it.connected || it.waiting {
				continue // finishBot asks for these, after the prompt
			}
			fmt.Fprintln(w.out, "\nGrok Bot")
			change, err := w.p.confirm("Change its webhook URL or key?", false)
			if err != nil {
				return err
			}
			if !change {
				continue
			}
			plan.connect = append(plan.connect, it)
			details, err := w.askBot("Webhook URL of the Grok Bot routine", false)
			if err != nil {
				return err
			}
			plan.bot = details
		}
	}
	return nil
}

// askBot asks for the webhook URL and key. If optional, leaving the URL empty
// returns nil.
func (w *wizard) askBot(question string, optional bool) (*botDetails, error) {
	d := &botDetails{}
	for {
		raw, err := w.p.line(question, "")
		if err != nil {
			return nil, err
		}
		if raw == "" && optional {
			return nil, nil
		}
		if validWebhookURL(raw) {
			d.url = raw
			break
		}
		fmt.Fprintln(w.out, "That doesn't look like a web address. It should start with https://")
	}

	saved := defaultBotKeyPath()
	if keyFileOK(saved) {
		use, err := w.p.confirm("Use the key saved in "+saved+"?", true)
		if err != nil {
			return nil, err
		}
		if use {
			d.keyFile = saved
			return d, nil
		}
	}
	for {
		path, err := w.p.line("Path to a file holding its key, or press Enter to paste the key", "")
		if err != nil {
			return nil, err
		}
		if path != "" {
			abs, err := filepath.Abs(expandHome(path, w.o.home))
			if err == nil && keyFileOK(abs) {
				d.keyFile = abs
				return d, nil
			}
			fmt.Fprintf(w.out, "Can't read a key from %s.\n", path)
			continue
		}
		key, err := w.p.secret("Paste the key (it won't be shown)")
		if err != nil {
			return nil, err
		}
		if key != "" {
			d.keyFile, d.pastedKey = saved, key
			return d, nil
		}
		fmt.Fprintln(w.out, "No key entered.")
	}
}

func validWebhookURL(raw string) bool {
	u, err := neturl.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func defaultBotKeyPath() string {
	return filepath.Join(filepath.Dir(configDir()), "grok-bot", "sender-key")
}

func keyFileOK(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(b)) != ""
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

func routinePath() string { return filepath.Join(configDir(), "grok-bot-routine.md") }

func (w *wizard) toolOptions(it *wizardItem, plan wizardPlan, remove bool) setupOptions {
	o := w.o
	o.as, o.remove, o.quiet = it.agent, remove, true
	if it.key == "hermes" {
		o.wake = plan.hermesWake
	}
	return o
}

func (w *wizard) preview(plan wizardPlan) {
	fmt.Fprintln(w.out, "\nChanges to make:")
	if plan.service {
		fmt.Fprintf(w.out, "\nBackground service:\nwould install it through %s and restart it\n", w.svc.name())
	}
	for _, it := range plan.connect {
		fmt.Fprintf(w.out, "\n%s:\n", it.label)
		if h, ok := headlessFor(it.key); ok {
			fmt.Fprintf(w.out, "would add agent %s, running `%s` in %s\n", h.agent, h.command, plan.headlessCwd[h.key])
			continue
		}
		switch it.key {
		case itemBot:
			if plan.bot == nil {
				fmt.Fprintf(w.out, "would add agent %s and save its token to %s\n", botAgent, filepath.Join(tokenDir(), botAgent))
				fmt.Fprintf(w.out, "would save the routine text to %s\n", routinePath())
				fmt.Fprintln(w.out, "then gives you a prompt for Grok Bot, and asks for the webhook URL and key it gives you")
				continue
			}
			fmt.Fprintf(w.out, "would update agent %s, delivering to %s\n", botAgent, urlHost(plan.bot.url))
			if plan.bot.pastedKey != "" {
				fmt.Fprintf(w.out, "would save the key you pasted to %s\n", plan.bot.keyFile)
			} else {
				fmt.Fprintf(w.out, "would read the key from %s\n", plan.bot.keyFile)
			}
		default:
			if err := setupOne(w.ctx, it.key, w.toolOptions(it, plan, false), true); err != nil {
				fmt.Fprintf(w.out, "can't preview: %v\n", err)
			}
		}
	}
	for _, it := range plan.disconnect {
		fmt.Fprintf(w.out, "\n%s (unticked):\n", it.label)
		if h, ok := headlessFor(it.key); ok {
			fmt.Fprintf(w.out, "would delete agent %s and its token\n", h.agent)
			continue
		}
		switch it.key {
		case itemBot:
			fmt.Fprintf(w.out, "would delete agent %s and its token; its routine would need a new token if you reconnect\n", botAgent)
		default:
			if err := setupOne(w.ctx, it.key, w.toolOptions(it, plan, true), true); err != nil {
				fmt.Fprintf(w.out, "can't preview: %v\n", err)
			}
		}
	}
}

// apply carries out every step, and keeps going after a failure.
func (w *wizard) apply(plan wizardPlan) (failed []string) {
	step := func(name string, run func() error) {
		fmt.Fprintf(w.out, "\n%s:\n", name)
		if err := run(); err != nil {
			fmt.Fprintf(w.out, "failed: %v\n", err)
			failed = append(failed, name)
		}
	}
	for _, it := range plan.disconnect {
		switch it.key {
		case itemHeadless, itemHermesHeadless, itemBot:
			step(it.label, func() error { return w.deleteAgent(it.agent) })
		default:
			step(it.label, func() error { return setupOne(w.ctx, it.key, w.toolOptions(it, plan, true), false) })
		}
	}
	for _, it := range plan.connect {
		if h, ok := headlessFor(it.key); ok {
			step(it.label, func() error { return w.addHeadless(h, plan.headlessCwd[h.key]) })
			continue
		}
		switch it.key {
		case itemBot:
			step(it.label, func() error {
				if plan.bot == nil {
					return w.registerBot()
				}
				return w.connectBot(*plan.bot)
			})
		default:
			step(it.label, func() error { return setupOne(w.ctx, it.key, w.toolOptions(it, plan, false), false) })
		}
	}
	if plan.service {
		step("Background service", func() error {
			if err := w.svc.install(w.o.exe, w.o.db); err != nil {
				return err
			}
			if !waitBus(w.ctx, w.url) {
				return fmt.Errorf("the service started, but the bus didn't answer at %s", w.url)
			}
			fmt.Fprintln(w.out, "running")
			return nil
		})
	}
	return failed
}

func (w *wizard) deleteAgent(name string) error {
	st, err := store.Open(w.o.db)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DeleteAgent(w.ctx, name); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(tokenDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if name == botAgent {
		os.Remove(botPromptPath()) // it holds the token just deleted
	}
	fmt.Fprintf(w.out, "deleted agent %s\n", name)
	return nil
}

func (w *wizard) addHeadless(h headlessAgent, cwd string) error {
	argv := strings.Fields(h.command)
	if _, err := exec.LookPath(argv[0]); err != nil {
		return err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	st, err := store.Open(w.o.db)
	if err != nil {
		return err
	}
	defer st.Close()
	err = createAgent(w.ctx, st, bus.Agent{
		Name: h.agent, Delivery: "acp", CreatedAt: time.Now().UTC(),
		Config: map[string]string{
			"command": h.command, "permission": "reject", "cwd": abs,
			"description": h.description, "label": h.label,
		},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(w.out, "added agent %s\n", h.agent)
	return nil
}

// registerBot adds Grok Bot and its token. It has no webhook until the prompt
// has been used, so it starts as a pull agent, which keeps messages waiting.
func (w *wizard) registerBot() error {
	st, err := store.Open(w.o.db)
	if err != nil {
		return err
	}
	defer st.Close()
	err = createAgent(w.ctx, st, bus.Agent{
		Name: botAgent, Delivery: bus.DeliveryPull, CreatedAt: time.Now().UTC(),
		Config: map[string]string{
			"description": "Grok Bot: personal assistant (email, calendar, web); answers in its own time",
			"label":       "Grok Bot",
		},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(w.out, "added agent %s\n", botAgent)
	r := &setupRun{stamp: w.o.stamp, home: w.o.home}
	return r.write(routinePath(), birdie.GrokBotRoutine)
}

// connectBot points Grok Bot at its webhook. Switching its delivery is what
// sends any messages that were waiting, so it comes last.
func (w *wizard) connectBot(d botDetails) error {
	r := &setupRun{stamp: w.o.stamp, home: w.o.home}
	if d.pastedKey != "" {
		if err := os.MkdirAll(filepath.Dir(d.keyFile), 0o700); err != nil {
			return err
		}
		if err := r.write(d.keyFile, d.pastedKey+"\n"); err != nil {
			return err
		}
	}
	st, err := store.Open(w.o.db)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetAgentConfig(w.ctx, botAgent, "url", d.url); err != nil {
		return err
	}
	if err := st.SetAgentConfig(w.ctx, botAgent, "auth_file", d.keyFile); err != nil {
		return err
	}
	if err := st.SetAgentDelivery(w.ctx, botAgent, "webhook"); err != nil {
		return err
	}
	fmt.Fprintf(w.out, "%s now delivers to %s\n", botAgent, urlHost(d.url))
	return nil
}

// botDelivery returns how Grok Bot gets its messages, or "" if it isn't registered.
func (w *wizard) botDelivery() string {
	st, err := store.Open(w.o.db)
	if err != nil {
		return ""
	}
	defer st.Close()
	a, err := st.GetAgent(w.ctx, botAgent)
	if err != nil {
		return ""
	}
	return a.Delivery
}

// botWaiting reports whether Grok Bot is registered but has no webhook yet.
func (w *wizard) botWaiting() bool { return w.botDelivery() == bus.DeliveryPull }

// finishBot gives the user Grok Bot's prompt, then asks for the webhook URL and
// key Grok Bot answers with. The changes are applied by now, so stopping here
// isn't an error: messages wait, and running setup again picks this up.
func (w *wizard) finishBot(resuming bool) error {
	later := func() {
		fmt.Fprintf(w.out, "\nMessages to %s will wait. To finish, run: birdie setup %s\n", botAgent, botAgent)
		fmt.Fprintf(w.out, "Get the prompt at any time with: birdie agent prompt %s\n", botAgent)
	}
	fmt.Fprintln(w.out, "\nFinish Grok Bot:")
	give := true
	if resuming {
		again, err := w.p.confirm("Do you need the prompt for Grok Bot again?", false)
		if err != nil {
			later()
			return nil
		}
		give = again
	}
	if give && !w.giveBotPrompt() {
		later()
		return nil
	}
	fmt.Fprintln(w.out, "\nOnce Grok Bot has set up the routine, it will give you the webhook URL and key.")
	d, err := w.askBot("Webhook URL from Grok Bot, or press Enter to finish later", true)
	if err != nil || d == nil {
		later()
		return nil
	}
	return w.connectBot(*d)
}

func (w *wizard) nextSteps(plan wizardPlan) {
	var steps []string
	for _, it := range plan.connect {
		if _, isTool := setupTools[it.key]; isTool {
			steps = append(steps, "Restart any open sessions of the tools you connected.")
			break
		}
	}
	for _, it := range append(plan.connect[:len(plan.connect):len(plan.connect)], plan.disconnect...) {
		if it.key == "hermes" {
			steps = append(steps, "Restart Hermes to load the change: hermes gateway restart.")
			break
		}
	}
	if len(steps) > 0 {
		fmt.Fprintln(w.out, "\nWhat's next:")
		for i, s := range steps {
			fmt.Fprintf(w.out, "  %d. %s\n", i+1, s)
		}
	}
}

// giveBotPrompt hands over the prompt in the terminal or as a file. It returns
// false if the user didn't get it.
func (w *wizard) giveBotPrompt() bool {
	prompt, err := botPrompt(w.url)
	if err != nil {
		fmt.Fprintf(w.out, "Can't build the prompt: %v\n", err)
		return false
	}
	fmt.Fprintln(w.out, "Give Grok Bot one prompt. It has the routine's instructions, the bus address and Grok Bot's token.")
	show, err := w.p.confirm("Show it here? It includes the token, so No saves it to a private file instead.", false)
	if err != nil {
		return false
	}
	if show {
		fmt.Fprintf(w.out, "\n----- Copy from the next line -----\n%s\n----- Copy up to the line above -----\n", prompt)
		fmt.Fprintln(w.out, "Paste it into a Grok Bot chat. Treat it like a password, because it includes the token.")
		return true
	}
	path, err := saveBotPrompt(prompt)
	if err != nil {
		fmt.Fprintf(w.out, "Can't save the prompt: %v\n", err)
		return false
	}
	fmt.Fprintf(w.out, "Saved to %s, where only you can read it.\n", path)
	fmt.Fprintln(w.out, "Paste or attach it in a Grok Bot chat, then delete the file, because it includes the token.")
	return true
}
