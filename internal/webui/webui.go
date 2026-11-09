// Package webui serves birdie's web app and the API behind it, at /ui on the
// bus's own address. It is for the machine's owner only: it answers on a
// loopback host, after signing in with a one-time link from `birdie ui`.
package webui

import (
	"embed"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"

	"birdie/internal/bus"
	"birdie/internal/store"
)

// Me is the agent the web app sends messages as. `birdie ui` creates it.
const Me = "me"

//go:embed all:dist
var dist embed.FS

// Options configure the web app.
type Options struct {
	Bus     *bus.Bus
	Store   *store.SQLite
	Log     *slog.Logger
	Version string
	// Addr is where the bus listens, shown in the app.
	Addr string
}

type server struct {
	Options
	files fs.FS
	built bool
}

// New returns the handler for /ui and everything under it.
func New(o Options) http.Handler {
	sub, _ := fs.Sub(dist, "dist")
	_, err := fs.Stat(sub, "index.html")
	s := &server{Options: o, files: sub, built: err == nil}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ui/", http.StatusMovedPermanently) })
	mux.HandleFunc("GET /ui/", s.static)
	mux.HandleFunc("POST /ui/api/login", s.login)
	mux.HandleFunc("POST /ui/api/logout", s.logout)
	mux.HandleFunc("GET /ui/api/me", s.signedIn(s.me))
	mux.HandleFunc("GET /ui/api/overview", s.signedIn(s.overview))
	mux.HandleFunc("GET /ui/api/messages", s.signedIn(s.recent))
	mux.HandleFunc("POST /ui/api/messages", s.signedIn(s.send))
	mux.HandleFunc("GET /ui/api/threads", s.signedIn(s.threads))
	mux.HandleFunc("GET /ui/api/threads/{id}", s.signedIn(s.thread))
	mux.HandleFunc("GET /ui/api/tools", s.signedIn(s.tools))
	mux.HandleFunc("GET /ui/api/events", s.signedIn(s.events))
	mux.HandleFunc("GET /ui/api/", func(w http.ResponseWriter, r *http.Request) { s.fail(w, http.StatusNotFound, "no such API") })
	return s.guard(mux)
}

// guard answers only on a loopback host, so a page elsewhere can't reach the
// app by pointing a hostname at this machine, and refuses writes from other
// origins.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if !loopbackHost(r.Host) {
			s.fail(w, http.StatusForbidden, "the web app only answers on 127.0.0.1 or localhost")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "http://"+r.Host && origin != "https://"+r.Host {
				s.fail(w, http.StatusForbidden, "request from another site refused")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// static serves the built app. Paths that aren't files get index.html, so the
// app's own links work on reload.
func (s *server) static(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", csp)
	if !s.built {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(fallbackPage))
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/ui")
	name = strings.TrimPrefix(name, "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	if fi, err := fs.Stat(s.files, name); err != nil || fi.IsDir() {
		name = "index.html"
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.files, name)
}

const fallbackPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>birdie</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:36rem;margin:4rem auto;padding:0 1rem;color:#0A2E2B;background:#DCF5E8}code{font-family:ui-monospace,monospace}</style></head>
<body><h1>The web app isn't built into this birdie</h1>
<p>This binary was built without the web app. Build it with <code>make web build</code>, or install a release from GitHub.</p></body></html>`
