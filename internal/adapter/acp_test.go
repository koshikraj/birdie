package adapter_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"birdie/internal/adapter"
	"birdie/internal/bus"
	"birdie/internal/store"
)

// The test binary doubles as a fake ACP agent when this variable names a
// directory to keep its sessions in.
const fakeEnv = "BIRDIE_FAKE_ACP_DIR"

func TestMain(m *testing.M) {
	if dir := os.Getenv(fakeEnv); dir != "" {
		fakeAgent(dir)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeAgent(dir string) {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := json.NewEncoder(os.Stdout)
	send := func(v map[string]any) { v["jsonrpc"] = "2.0"; out.Encode(v) }
	chunk := func(sid, text string) {
		send(map[string]any{"method": "session/update", "params": map[string]any{
			"sessionId": sid, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
	}
	for in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
				Prompt    []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
		}
		json.Unmarshal(in.Bytes(), &req)
		path := filepath.Join(dir, req.Params.SessionID)
		switch req.Method {
		case "initialize":
			send(map[string]any{"id": req.ID, "result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]bool{"loadSession": true}}})
		case "session/new":
			sid := fmt.Sprintf("s-%d", time.Now().UnixNano())
			os.WriteFile(filepath.Join(dir, sid), nil, 0o600)
			send(map[string]any{"id": req.ID, "result": map[string]string{"sessionId": sid}})
		case "session/load":
			if _, err := os.Stat(path); err != nil {
				send(map[string]any{"id": req.ID, "error": map[string]any{"code": -32603, "message": "no such session"}})
				continue
			}
			chunk(req.Params.SessionID, "REPLAYED HISTORY ")
			send(map[string]any{"id": req.ID, "result": nil})
		case "session/prompt":
			text := req.Params.Prompt[0].Text
			if strings.Contains(text, "needs-permission") {
				send(map[string]any{"id": "perm-1", "method": "session/request_permission", "params": map[string]any{
					"sessionId": req.Params.SessionID,
					"options": []map[string]string{
						{"optionId": "yes", "kind": "allow_once"}, {"optionId": "no", "kind": "reject_once"}}}})
				in.Scan()
				var resp struct {
					Result struct {
						Outcome struct {
							OptionID string `json:"optionId"`
						} `json:"outcome"`
					} `json:"result"`
				}
				json.Unmarshal(in.Bytes(), &resp)
				chunk(req.Params.SessionID, "permission: "+resp.Result.Outcome.OptionID)
			} else if strings.Contains(text, "which-agent") {
				chunk(req.Params.SessionID, "agent: "+os.Getenv(adapter.ACPAgentEnv))
			} else {
				f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
				f.WriteString("x")
				f.Close()
				b, _ := os.ReadFile(path)
				chunk(req.Params.SessionID, fmt.Sprintf("turn %d ", len(b)))
				chunk(req.Params.SessionID, "in "+req.Params.SessionID)
			}
			send(map[string]any{"id": req.ID, "result": map[string]string{"stopReason": "end_turn"}})
		}
	}
}

func setupACP(t *testing.T, cfg map[string]string) (*bus.Bus, context.Context) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeEnv, t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "bus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acp := &adapter.ACP{State: st, Log: log}
	b := bus.New(st, log, acp)
	acp.Bus = b
	acp.Start(ctx)
	go b.RunDelivery(ctx)

	config := map[string]string{"command": exe, "cwd": t.TempDir()}
	for k, v := range cfg {
		config[k] = v
	}
	for _, a := range []bus.Agent{
		{Name: "alice", Delivery: bus.DeliveryPull},
		{Name: "grok", Delivery: "acp", Config: config},
	} {
		a.TokenHash, a.CreatedAt = bus.HashToken(a.Name), time.Now()
		if err := st.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	return b, ctx
}

func reply(t *testing.T, b *bus.Bus, ctx context.Context, thread string) bus.Message {
	t.Helper()
	got, err := b.Receive(ctx, "alice", thread, 20*time.Second)
	if err != nil || len(got) != 1 {
		t.Fatalf("alice inbox = %+v, %v", got, err)
	}
	return got[0]
}

func TestACPThreadKeepsSession(t *testing.T) {
	b, ctx := setupACP(t, nil)

	first, err := b.Send(ctx, "alice", bus.SendInput{To: "grok", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	r1 := reply(t, b, ctx, first.ThreadID)
	if r1.From != "grok" || r1.InReplyTo != first.ID || !strings.HasPrefix(r1.Content, "turn 1 in s-") {
		t.Fatalf("first reply = %+v", r1)
	}
	if r1.Meta["acp_stop_reason"] != "end_turn" {
		t.Fatalf("meta = %v", r1.Meta)
	}
	session := strings.TrimPrefix(r1.Content, "turn 1 in ")

	second, err := b.Send(ctx, "alice", bus.SendInput{To: "grok", Content: "again", InReplyTo: r1.ID})
	if err != nil {
		t.Fatal(err)
	}
	r2 := reply(t, b, ctx, first.ThreadID)
	if r2.Content != "turn 2 in "+session {
		t.Fatalf("second reply = %q, want turn 2 in %s (replayed history must be ignored)", r2.Content, session)
	}
	if r2.InReplyTo != second.ID {
		t.Fatalf("second reply answers %s, want %s", r2.InReplyTo, second.ID)
	}

	other, _ := b.Send(ctx, "alice", bus.SendInput{To: "grok", Content: "new topic"})
	if r3 := reply(t, b, ctx, other.ThreadID); strings.HasSuffix(r3.Content, session) {
		t.Fatalf("a new thread reused session %s", session)
	}
}

func TestACPPermission(t *testing.T) {
	for policy, want := range map[string]string{"reject": "permission: no", "allow": "permission: yes"} {
		t.Run(policy, func(t *testing.T) {
			b, ctx := setupACP(t, map[string]string{"permission": policy})
			m, _ := b.Send(ctx, "alice", bus.SendInput{To: "grok", Content: "needs-permission"})
			if r := reply(t, b, ctx, m.ThreadID); r.Content != want {
				t.Fatalf("reply = %q, want %q", r.Content, want)
			}
		})
	}
}

// The agent's own hooks can tell they run in a headless turn birdie started.
func TestACPNamesTheAgentInItsEnvironment(t *testing.T) {
	b, ctx := setupACP(t, nil)
	m, _ := b.Send(ctx, "alice", bus.SendInput{To: "grok", Content: "which-agent"})
	if r := reply(t, b, ctx, m.ThreadID); r.Content != "agent: grok" {
		t.Fatalf("reply = %q", r.Content)
	}
}

func TestACPReplyLimit(t *testing.T) {
	b, ctx := setupACP(t, map[string]string{"max_replies": "1"})
	m, _ := b.Send(ctx, "alice", bus.SendInput{To: "grok", Content: "one"})
	r := reply(t, b, ctx, m.ThreadID)
	b.Send(ctx, "alice", bus.SendInput{To: "grok", Content: "two", InReplyTo: r.ID})
	got, _ := b.Receive(ctx, "alice", m.ThreadID, 5*time.Second)
	if len(got) != 1 || got[0].From != bus.SystemSender || !strings.Contains(got[0].Content, "limit") {
		t.Fatalf("want a notice about the reply limit, got %+v", got)
	}
}

func TestACPAgentFailureIsReported(t *testing.T) {
	b, ctx := setupACP(t, map[string]string{"command": "/nonexistent/acp-agent"})
	m, _ := b.Send(ctx, "alice", bus.SendInput{To: "grok", Content: "hi"})
	r := reply(t, b, ctx, m.ThreadID)
	if r.Meta["acp_error"] != "true" || !strings.Contains(r.Content, "could not get an answer") {
		t.Fatalf("reply = %+v", r)
	}
}
