package webui_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
	"birdie/internal/webui"
)

type rig struct {
	t      *testing.T
	srv    *httptest.Server
	st     *store.SQLite
	b      *bus.Bus
	cookie *http.Cookie
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	b := bus.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, name := range []string{webui.Me, "bob"} {
		if err := st.CreateAgent(context.Background(), bus.Agent{Name: name, TokenHash: name, Delivery: bus.DeliveryPull, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(webui.New(webui.Options{Bus: b, Store: st, Version: "test", Addr: "127.0.0.1:7777"}))
	t.Cleanup(srv.Close)
	return &rig{t: t, srv: srv, st: st, b: b}
}

func (r *rig) do(method, path, body string, hdr map[string]string) *http.Response {
	r.t.Helper()
	req, _ := http.NewRequest(method, r.srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", r.srv.URL)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	if r.cookie != nil {
		req.AddCookie(r.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	return resp
}

func (r *rig) signIn() {
	r.t.Helper()
	code, err := webui.NewLoginCode(context.Background(), r.st)
	if err != nil {
		r.t.Fatal(err)
	}
	resp := r.do("POST", "/ui/api/login", `{"code":"`+code+`"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		r.t.Fatalf("login: %s", resp.Status)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "birdie_ui" {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				r.t.Fatalf("cookie = %+v", c)
			}
			r.cookie = c
		}
	}
	if r.cookie == nil {
		r.t.Fatal("no session cookie")
	}
	// A code works once.
	again := r.do("POST", "/ui/api/login", `{"code":"`+code+`"}`, nil)
	again.Body.Close()
	if again.StatusCode != http.StatusUnauthorized {
		r.t.Fatalf("second login: %s", again.Status)
	}
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestSignInAndGuards(t *testing.T) {
	r := newRig(t)
	if resp := r.do("GET", "/ui/api/overview", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("overview before sign-in: %s", resp.Status)
	}
	if resp := r.do("GET", "/ui/api/me", "", map[string]string{"Host": "evil.example:7777"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("another host: %s", resp.Status)
	}
	if resp := r.do("POST", "/ui/api/login", `{"code":"nope"}`, map[string]string{"Origin": "https://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a write from another site: %s", resp.Status)
	}
	if resp := r.do("POST", "/ui/api/login", `{"code":"nope"}`, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong code: %s", resp.Status)
	}
	r.signIn()
	if resp := r.do("GET", "/ui/api/me", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("me: %s", resp.Status)
	}
	if resp := r.do("POST", "/ui/api/messages", `{"to":"bob","content":"x"}`, map[string]string{"Origin": ""}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a write with no origin: %s", resp.Status)
	}
	resp := r.do("POST", "/ui/api/logout", "", nil)
	resp.Body.Close()
	if resp := r.do("GET", "/ui/api/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout: %s", resp.Status)
	}
}

func TestSendShowsUpEverywhere(t *testing.T) {
	r := newRig(t)
	r.signIn()
	ctx := context.Background()
	if err := r.b.ReportSession(ctx, "bob", "s1", bus.SessionInput{Name: "api", Tier: bus.TierPush, State: bus.StateIdle}); err != nil {
		t.Fatal(err)
	}

	// Listen for events before sending.
	stream := r.do("GET", "/ui/api/events", "", nil)
	defer stream.Body.Close()
	if ct := stream.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := bufio.NewScanner(stream.Body)

	var sent struct {
		ID       string
		ThreadID string `json:"thread_id"`
		Note     string
	}
	resp := r.do("POST", "/ui/api/messages", `{"to":"bob","content":"hello bob"}`, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("send: %s", resp.Status)
	}
	decode(t, resp, &sent)
	if !strings.Contains(sent.Note, "bob · api") {
		t.Fatalf("note = %q", sent.Note)
	}

	deadline := time.After(5 * time.Second)
	got := make(chan string, 1)
	go func() {
		for lines.Scan() {
			if line := lines.Text(); strings.HasPrefix(line, "data: ") && strings.Contains(line, sent.ID) {
				got <- line
				return
			}
		}
	}()
	select {
	case line := <-got:
		if !strings.Contains(line, `"type":"queued"`) || !strings.Contains(line, `"to_label":"bob"`) || !strings.Contains(line, `"text":"hello bob"`) {
			t.Fatalf("event = %s", line)
		}
	case <-deadline:
		t.Fatal("no event for the sent message")
	}

	var ov struct {
		Me     string
		Agents []struct {
			Name     string
			Sessions []struct {
				Name  string
				State string
				Live  bool
			}
			Health struct{ Waiting int }
		}
		Waiting struct{ Total int }
		Today   struct{ Sent int }
	}
	decode(t, r.do("GET", "/ui/api/overview", "", nil), &ov)
	var bob bool
	for _, a := range ov.Agents {
		if a.Name == "bob" {
			bob = len(a.Sessions) == 1 && a.Sessions[0].Live && a.Sessions[0].State == "idle" && a.Health.Waiting == 1
		}
	}
	if ov.Me != webui.Me || !bob || ov.Waiting.Total != 1 || ov.Today.Sent != 1 {
		t.Fatalf("overview = %+v", ov)
	}

	var th struct {
		ID       string
		Messages []struct {
			ID        string
			FromLabel string `json:"from_label"`
		}
	}
	decode(t, r.do("GET", "/ui/api/threads/"+sent.ID, "", nil), &th)
	if th.ID != sent.ThreadID || len(th.Messages) != 1 || th.Messages[0].FromLabel != webui.Me {
		t.Fatalf("thread = %+v", th)
	}
	// An answer to you is delivered once you open its thread.
	if _, err := r.b.Send(ctx, "bob", bus.SendInput{InReplyTo: sent.ID, Content: "hi back"}); err != nil {
		t.Fatal(err)
	}
	var withAnswer struct {
		Messages []struct{ To, Status string }
	}
	decode(t, r.do("GET", "/ui/api/threads/"+sent.ThreadID, "", nil), &withAnswer)
	if n := len(withAnswer.Messages); n != 2 || withAnswer.Messages[1].To != webui.Me || withAnswer.Messages[1].Status != bus.StatusDelivered {
		t.Fatalf("answer = %+v", withAnswer.Messages)
	}
	if resp := r.do("GET", "/ui/api/threads/nope", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown thread: %s", resp.Status)
	}
	if resp := r.do("POST", "/ui/api/messages", `{"to":"nobody","content":"x"}`, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown agent: %s", resp.Status)
	}
}

func TestAppPagesFallBackToIndex(t *testing.T) {
	r := newRig(t)
	resp := r.do("GET", "/ui/agents", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("page: %s %s", resp.Status, resp.Header.Get("Content-Type"))
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("csp = %q", csp)
	}
}
