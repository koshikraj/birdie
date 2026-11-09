package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

// The JSON shapes here are the contract in web/src/api/types.ts.

type sessionJSON struct {
	bus.Session
	Live bool `json:"live"`
}

type healthJSON struct {
	Waiting   int        `json:"waiting"`
	Oldest    *time.Time `json:"oldest"`
	Delivered *time.Time `json:"delivered"`
	Error     string     `json:"error"`
	ErrorID   string     `json:"error_id"`
	ErrorAt   *time.Time `json:"error_at"`
	Retry     *time.Time `json:"retry"`
	GaveUp    int        `json:"gave_up"`
}

type agentJSON struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Label       string        `json:"label"`
	Delivery    string        `json:"delivery"`
	CreatedAt   time.Time     `json:"created_at"`
	Sessions    []sessionJSON `json:"sessions"`
	Health      healthJSON    `json:"health"`
}

type trackedJSON struct {
	ID          string     `json:"id"`
	ThreadID    string     `json:"thread_id"`
	InReplyTo   string     `json:"in_reply_to,omitempty"`
	From        string     `json:"from"`
	To          string     `json:"to"`
	FromSession string     `json:"from_session,omitempty"`
	ToSession   string     `json:"to_session,omitempty"`
	FromLabel   string     `json:"from_label"`
	ToLabel     string     `json:"to_label"`
	Content     string     `json:"content"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
	TakenBy     string     `json:"taken_by"`
	Attempts    int        `json:"attempts"`
	Error       string     `json:"error"`
	ErrorAt     *time.Time `json:"error_at"`
	Retry       *time.Time `json:"retry"`
}

func when(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// labels names sessions as their address, agent/name.
type labels map[string]string

func (s *server) labels(agents []bus.Agent) labels {
	l := labels{}
	for _, a := range agents {
		for _, ses := range a.Sessions {
			name := ses.Name
			if name == "" {
				name = ses.ID
			}
			l[a.Name+"/"+ses.ID] = a.Name + "/" + name
		}
	}
	return l
}

func (l labels) of(agent, session string) string {
	if session == "" {
		return agent
	}
	if v, ok := l[agent+"/"+session]; ok {
		return v
	}
	if len(session) > 8 {
		session = session[:8]
	}
	return agent + "/" + session
}

func (l labels) track(t store.Tracked) trackedJSON {
	taken := ""
	if t.TakenBy != "" {
		taken = l.of(t.To, t.TakenBy)
	}
	return trackedJSON{
		ID: t.ID, ThreadID: t.ThreadID, InReplyTo: t.InReplyTo, From: t.From, To: t.To,
		FromSession: t.FromSession, ToSession: t.ToSession,
		FromLabel: l.of(t.From, t.FromSession), ToLabel: l.of(t.To, t.ToSession),
		Content: t.Content, Status: t.Status, CreatedAt: t.CreatedAt, DeliveredAt: when(t.Delivered),
		TakenBy: taken, Attempts: t.Attempts, Error: t.Error, ErrorAt: when(t.ErrorAt), Retry: when(t.Retry),
	}
}

func (l labels) tracks(ts []store.Tracked) []trackedJSON {
	out := make([]trackedJSON, len(ts))
	for i, t := range ts {
		out[i] = l.track(t)
	}
	return out
}

func startOfDay(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agents, err := s.Bus.AgentsWithRecentSessions(ctx)
	if err != nil {
		s.internal(w, err)
		return
	}
	now := time.Now()
	deliveries, err := s.Store.Deliveries(ctx, now.Add(-24*time.Hour))
	if err != nil {
		s.internal(w, err)
		return
	}
	waiting, total, err := s.Store.Waiting(ctx, 10)
	if err != nil {
		s.internal(w, err)
		return
	}
	day := startOfDay(now)
	pairs, err := s.Store.PairsSince(ctx, day)
	if err != nil {
		s.internal(w, err)
		return
	}
	sent, last, err := s.Store.SentSince(ctx, day)
	if err != nil {
		s.internal(w, err)
		return
	}
	l := s.labels(agents)
	out := make([]agentJSON, 0, len(agents))
	for _, a := range agents {
		if bus.IsSystemSender(a.Name) {
			continue
		}
		d := deliveries[a.Name]
		aj := agentJSON{
			Name: a.Name, Description: a.Description, Label: a.Label, Delivery: a.Delivery, CreatedAt: a.CreatedAt, Sessions: []sessionJSON{},
			Health: healthJSON{
				Waiting: d.Waiting, Oldest: when(d.Oldest), Delivered: when(d.Delivered),
				Error: d.Error, ErrorID: d.ErrorID, ErrorAt: when(d.ErrorAt), Retry: when(d.Retry), GaveUp: d.GaveUp,
			},
		}
		for _, ses := range a.Sessions {
			aj.Sessions = append(aj.Sessions, sessionJSON{Session: ses, Live: ses.Live(now)})
		}
		out = append(out, aj)
	}
	pj := make([]map[string]any, len(pairs))
	for i, p := range pairs {
		pj[i] = map[string]any{"a": p.A, "b": p.B, "count": p.Count}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.Version, "address": s.Addr, "me": Me, "live_ttl_seconds": int(bus.SessionTTL.Seconds()),
		"agents":  out,
		"waiting": map[string]any{"total": total, "messages": l.tracks(waiting)},
		"pairs":   pj,
		"today":   map[string]any{"sent": sent, "last_at": when(last)},
	})
}

func limit(r *http.Request, def, most int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, most)
}

func (s *server) currentLabels(r *http.Request) labels {
	agents, err := s.Bus.AgentsWithRecentSessions(r.Context())
	if err != nil {
		return labels{}
	}
	return s.labels(agents)
}

func (s *server) recent(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.Store.RecentMessages(r.Context(), limit(r, 20, 100))
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": s.currentLabels(r).tracks(msgs)})
}

func (s *server) threads(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.RecentThreads(r.Context(), limit(r, 30, 200))
	if err != nil {
		s.internal(w, err)
		return
	}
	l := s.currentLabels(r)
	out := make([]map[string]any, len(list))
	for i, t := range list {
		last := l.track(t.Last)
		out[i] = map[string]any{
			"id": t.ID, "participants": t.Participants, "count": t.Count, "last_at": t.Last.CreatedAt,
			"last_from": last.FromLabel, "last_text": t.Last.Content, "status": t.Last.Status, "problem": t.Problem,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": out})
}

// thread takes a thread id, or a message id from which it finds the thread.
func (s *server) thread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	msgs, err := s.Store.TrackThread(ctx, id)
	if err == nil && len(msgs) == 0 {
		var m bus.Message
		if m, err = s.Store.GetMessage(ctx, id); err == nil {
			id = m.ThreadID
			msgs, err = s.Store.TrackThread(ctx, id)
		}
	}
	if errors.Is(err, bus.ErrNotFound) || (err == nil && len(msgs) == 0) {
		s.fail(w, http.StatusNotFound, "no thread or message "+strconv.Quote(r.PathValue("id")))
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	// Answers to you are delivered once you open their thread here.
	for _, m := range msgs {
		if m.To == Me && m.Status == bus.StatusQueued {
			if _, err := s.Bus.ReceiveFor(ctx, bus.ReceiveInput{Agent: Me, Thread: id}); err != nil {
				s.internal(w, err)
				return
			}
			if msgs, err = s.Store.TrackThread(ctx, id); err != nil {
				s.internal(w, err)
				return
			}
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "messages": s.currentLabels(r).tracks(msgs)})
}

func (s *server) send(w http.ResponseWriter, r *http.Request) {
	var in struct{ To, Content string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, bus.MaxContentBytes+4096)).Decode(&in); err != nil {
		s.fail(w, http.StatusBadRequest, "the request isn't valid JSON")
		return
	}
	ctx := r.Context()
	if _, err := s.Store.GetAgent(ctx, Me); errors.Is(err, bus.ErrNotFound) {
		s.fail(w, http.StatusConflict, "the "+Me+" agent doesn't exist yet; run birdie ui once to set it up")
		return
	}
	m, err := s.Bus.Send(ctx, Me, bus.SendInput{To: in.To, Content: in.Content})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, bus.ErrInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, bus.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, bus.ErrForbidden):
			status = http.StatusForbidden
		case errors.Is(err, bus.ErrLoop):
			status = http.StatusTooManyRequests
		}
		if status == http.StatusInternalServerError {
			s.internal(w, err)
			return
		}
		s.fail(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": m.ID, "thread_id": m.ThreadID, "to": m.To, "to_session": m.ToSession, "status": m.Status, "note": m.Note,
	})
}

// tools reports which agent programs are on this machine and connected.
func (s *server) tools(w http.ResponseWriter, r *http.Request) {
	known := []struct{ binary, name, product, setup string }{
		{"claude", "claude", "Claude Code", "birdie setup claude"},
		{"codex", "codex", "Codex", "birdie setup codex"},
		{"grok", "grok-tui", "Grok CLI", "birdie setup grok"},
		{"hermes", "hermes", "Hermes Agent", "birdie setup hermes"},
	}
	out := make([]map[string]any, len(known))
	for i, k := range known {
		_, lookErr := exec.LookPath(k.binary)
		_, agentErr := s.Store.GetAgent(r.Context(), k.name)
		out[i] = map[string]any{"name": k.name, "product": k.product, "found": lookErr == nil, "connected": agentErr == nil, "setup": k.setup}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": out})
}

// events streams bus events as Server-Sent Events.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	// The server's write timeout is for ordinary requests; this one stays open.
	rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": birdie\n\n")
	rc.Flush()

	ch, stop := s.Bus.Watch()
	defer stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	l := s.currentLabels(r)
	fresh := time.Now()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		case e := <-ch:
			// Session names change rarely; refresh them now and then.
			if time.Since(fresh) > 2*time.Second || e.Kind == "session" {
				l = s.currentLabels(r)
				fresh = time.Now()
			}
			var data any
			if e.Kind == "session" {
				data = map[string]any{"kind": "session", "type": e.Type, "agent": e.Agent, "id": e.Session, "name": e.Name, "state": e.State, "at": e.At}
			} else {
				data = map[string]any{
					"kind": "message", "type": e.Type, "id": e.ID, "thread_id": e.ThreadID, "in_reply_to": e.InReplyTo,
					"from": e.From, "to": e.To, "from_label": l.of(e.From, e.FromSession), "to_label": l.of(e.To, e.ToSession),
					"text": e.Text, "error": e.Error, "at": e.At,
				}
			}
			b, _ := json.Marshal(data)
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, b); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *server) fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *server) internal(w http.ResponseWriter, err error) {
	if s.Log != nil {
		s.Log.Error("web app request failed", "err", err)
	}
	s.fail(w, http.StatusInternalServerError, "internal error")
}
