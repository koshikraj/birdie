package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"birdie/internal/bus"
	"birdie/internal/httpapi"
	"birdie/internal/store"
	"birdie/internal/webui"
)

// routes is everything `birdie serve` answers: the agents' API, and the web
// app at /ui.
func routes(b *bus.Bus, st *store.SQLite, log *slog.Logger, addr string) http.Handler {
	ui := webui.New(webui.Options{Bus: b, Store: st, Log: log, Version: version, Addr: addr})
	mux := http.NewServeMux()
	mux.Handle("/ui", ui)
	mux.Handle("/ui/", ui)
	mux.Handle("/", httpapi.New(b, log))
	return mux
}

// uiCmd opens the web app, signed in with a link that works once.
func uiCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	db := fs.String("db", defaultDB(), "SQLite database path (the one birdie serve uses)")
	url := fs.String("url", envOr("BIRDIE_URL", defaultURL), "birdie base URL")
	noOpen := fs.Bool("no-open", false, "print the sign-in link instead of opening a browser")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	base := strings.TrimRight(*url, "/")
	switch uiServed(ctx, base) {
	case uiDown:
		return fmt.Errorf("birdie isn't running at %s; start it with: birdie service install, or birdie serve", base)
	case uiMissing:
		return fmt.Errorf("the birdie running at %s doesn't have the web app yet; restart it with this version (birdie service install, or birdie serve)", base)
	}
	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := ensureMe(ctx, st); err != nil {
		return err
	}
	code, err := webui.NewLoginCode(ctx, st)
	if err != nil {
		return err
	}
	link := base + "/ui/#login=" + code
	if *noOpen || !openBrowser(link) {
		fmt.Printf("Open this link to sign in to the birdie web app. It works once, within 10 minutes:\n%s\n", link)
		return nil
	}
	fmt.Printf("Opened the birdie web app at %s/ui/\n", base)
	return nil
}

type uiState int

const (
	uiUp uiState = iota
	uiDown
	uiMissing
)

// uiServed checks that the bus at base is running and serves the web app: an
// older birdie answers 404 there.
func uiServed(ctx context.Context, base string) uiState {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ui/api/me", nil)
	if err != nil {
		return uiDown
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return uiDown
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return uiMissing
	}
	return uiUp
}

// ensureMe adds the agent the web app sends as, the first time.
func ensureMe(ctx context.Context, st *store.SQLite) error {
	_, err := st.GetAgent(ctx, webui.Me)
	if err == nil || !errors.Is(err, bus.ErrNotFound) {
		return err
	}
	a := bus.Agent{Name: webui.Me, Delivery: bus.DeliveryPull, CreatedAt: time.Now().UTC(),
		Config: map[string]string{"description": "You, from the birdie web app or the CLI (birdie send --as me)", "label": "You"}}
	if err := createAgent(ctx, st, a); err != nil {
		return err
	}
	fmt.Printf("added agent %s, which the web app sends as\n", webui.Me)
	return nil
}

func openBrowser(link string) bool {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	go cmd.Wait()
	return true
}
