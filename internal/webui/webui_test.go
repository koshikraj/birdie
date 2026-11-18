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

func TestSendKeepsTheConversationInItsThread(t *testing.T) {
	r := newRig(t)
	r.signIn()
	ctx := context.Background()
	for _, s := range []struct{ id, name string }{{"s1", "api"}, {"s2", "web"}} {
		if err := r.b.ReportSession(ctx, "bob", s.id, bus.SessionInput{Name: s.name, Tier: bus.TierPush}); err != nil {
			t.Fatal(err)
		}
	}
	type sendResult struct {
		ID        string
		ThreadID  string `json:"thread_id"`
		ToSession string `json:"to_session"`
	}
	send := func(body string, want int) sendResult {
		t.Helper()
		resp := r.do("POST", "/ui/api/messages", body, nil)
		if resp.StatusCode != want {
			resp.Body.Close()
			t.Fatalf("send %s: %s, want %d", body, resp.Status, want)
		}
		var out sendResult
		if want == http.StatusCreated {
			decode(t, resp, &out)
		} else {
			resp.Body.Close()
		}
		return out
	}

	first := send(`{"to":"bob/web","content":"first"}`, http.StatusCreated)
	answer, err := r.b.Send(ctx, "bob", bus.SendInput{InReplyTo: first.ID, Content: "answer", FromSession: "s2"})
	if err != nil {
		t.Fatal(err)
	}

	// A follow-up to the answer stays in the thread and goes to the session that answered.
	next := send(`{"to":"bob","content":"follow-up","in_reply_to":"`+answer.ID+`"}`, http.StatusCreated)
	if next.ThreadID != first.ThreadID || next.ToSession != "s2" {
		t.Fatalf("follow-up = %+v, want thread %s and session s2", next, first.ThreadID)
	}
	// Another one, before bob answers, follows your own message.
	again := send(`{"to":"bob","content":"and also","in_reply_to":"`+next.ID+`"}`, http.StatusCreated)
	if again.ThreadID != first.ThreadID || again.ToSession != "s2" {
		t.Fatalf("second follow-up = %+v", again)
	}
	var th struct {
		Messages []struct{ Content, Status string }
	}
	decode(t, r.do("GET", "/ui/api/threads/"+first.ThreadID, "", nil), &th)
	if len(th.Messages) != 4 || th.Messages[0].Status != bus.StatusReplied {
		t.Fatalf("thread = %+v", th.Messages)
	}

	// Writing to an agent that isn't in the thread brings it in.
	for _, name := range []string{"carol", "dave"} {
		if err := r.st.CreateAgent(ctx, bus.Agent{Name: name, TokenHash: name, Delivery: bus.DeliveryPull, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	joined := send(`{"to":"carol","content":"join us","thread_id":"`+first.ThreadID+`"}`, http.StatusCreated)
	if joined.ThreadID != first.ThreadID {
		t.Fatalf("carol's message went to thread %s, want %s", joined.ThreadID, first.ThreadID)
	}
	if _, err := r.b.Send(ctx, "carol", bus.SendInput{ThreadID: first.ThreadID, To: webui.Me, Content: "here"}); err != nil {
		t.Fatalf("carol can't post in the thread she joined: %v", err)
	}

	// The web app can't post into a thread it isn't in.
	other, err := r.b.Send(ctx, "bob", bus.SendInput{To: "dave", Content: "private"})
	if err != nil {
		t.Fatal(err)
	}
	send(`{"to":"bob","content":"x","thread_id":"`+other.ThreadID+`"}`, http.StatusForbidden)
	send(`{"to":"bob","content":"x","in_reply_to":"`+other.ID+`"}`, http.StatusForbidden)
}

func TestThreadTitlesInTheWebApp(t *testing.T) {
	r := newRig(t)
	r.signIn()
	ctx := context.Background()
	resp := r.do("POST", "/ui/api/messages", `{"to":"bob","content":"Plan the release\nsteps follow"}`, nil)
	var sent struct {
		ThreadID string `json:"thread_id"`
	}
	decode(t, resp, &sent)

	var list struct{ Threads []struct{ ID, Title string } }
	decode(t, r.do("GET", "/ui/api/threads", "", nil), &list)
	if len(list.Threads) != 1 || list.Threads[0].Title != "Plan the release" {
		t.Fatalf("threads = %+v", list.Threads)
	}

	if resp := r.do("PATCH", "/ui/api/threads/"+sent.ThreadID, `{"title":"v0.6.0 release"}`, map[string]string{"Origin": ""}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rename with no origin: %s", resp.Status)
	}
	if resp := r.do("PATCH", "/ui/api/threads/"+sent.ThreadID, `{"title":"   "}`, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty title: %s", resp.Status)
	}
	if resp := r.do("PATCH", "/ui/api/threads/"+sent.ThreadID, `{"title":"v0.6.0 release"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: %s", resp.Status)
	}
	var th struct {
		Title     string
		RenamedBy string `json:"renamed_by"`
	}
	decode(t, r.do("GET", "/ui/api/threads/"+sent.ThreadID, "", nil), &th)
	if th.Title != "v0.6.0 release" || th.RenamedBy == "" {
		t.Fatalf("thread = %+v", th)
	}

	// Not a thread the web app is in.
	if err := r.st.CreateAgent(ctx, bus.Agent{Name: "carol", TokenHash: "carol", Delivery: bus.DeliveryPull, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	other, err := r.b.Send(ctx, "bob", bus.SendInput{To: "carol", Content: "private"})
	if err != nil {
		t.Fatal(err)
	}
	if resp := r.do("PATCH", "/ui/api/threads/"+other.ThreadID, `{"title":"mine"}`, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("rename someone else's thread: %s", resp.Status)
	}
}

// Pages asked for on localhost move to 127.0.0.1, where birdie ui signs you
// in; the API and 127.0.0.1 itself stay put.
func TestLocalhostPagesMoveTo127(t *testing.T) {
	r := newRig(t)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path, host string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", r.srv.URL+path, nil)
		req.Host = host
		resp, err := noFollow.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	resp := get("/ui/messages?x=1", "localhost:7777")
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != "http://127.0.0.1:7777/ui/messages?x=1" {
		t.Fatalf("localhost page: %s to %q", resp.Status, resp.Header.Get("Location"))
	}
	if resp := get("/ui/", "localhost"); resp.Header.Get("Location") != "http://127.0.0.1/ui/" {
		t.Fatalf("localhost without a port: %s to %q", resp.Status, resp.Header.Get("Location"))
	}
	if resp := get("/ui/api/me", "localhost:7777"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("API on localhost: %s, want 401 without a redirect", resp.Status)
	}
	if resp := get("/ui/messages", "127.0.0.1:7777"); resp.StatusCode != http.StatusOK {
		t.Fatalf("page on 127.0.0.1: %s", resp.Status)
	}
}
