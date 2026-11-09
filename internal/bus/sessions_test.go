package bus_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"birdie/internal/bus"
)

func report(t *testing.T, b *bus.Bus, agent, id, name, tier string, active time.Time) {
	t.Helper()
	if err := b.ReportSession(context.Background(), agent, id, bus.SessionInput{Name: name, Tier: tier, ActiveAt: active}); err != nil {
		t.Fatal(err)
	}
}

func take(t *testing.T, b *bus.Bus, agent, session string) []bus.Message {
	t.Helper()
	msgs, err := b.ReceiveFor(context.Background(), bus.ReceiveInput{Agent: agent, Session: session, Lease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestParseAddress(t *testing.T) {
	for in, want := range map[string]bus.Address{
		"claude":            {Agent: "claude"},
		"claude/api":        {Agent: "claude", Session: "api"},
		"claude/api@laptop": {Agent: "claude", Session: "api", Node: "laptop"},
		"claude@laptop":     {Agent: "claude", Node: "laptop"},
	} {
		got, err := bus.ParseAddress(in)
		if err != nil || got != want || got.String() != in {
			t.Errorf("ParseAddress(%q) = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "/x", "claude/", "claude@", "claude/a b"} {
		if _, err := bus.ParseAddress(bad); !errors.Is(err, bus.ErrInvalid) {
			t.Errorf("ParseAddress(%q): err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestOtherMachinesAreRefusedForNow(t *testing.T) {
	b, st := newBus(t)
	addAgent(t, st, "alice", nil)
	addAgent(t, st, "bob", nil)
	_, err := b.Send(context.Background(), "alice", bus.SendInput{To: "bob@laptop", Content: "hi"})
	if !errors.Is(err, bus.ErrInvalid) || !strings.Contains(err.Error(), "this machine") {
		t.Fatalf("err = %v", err)
	}
}

func TestSystemSenderIsReserved(t *testing.T) {
	if bus.ValidName(bus.SystemSender) {
		t.Fatal("an agent must not be able to take the bus's own name")
	}
}

func TestNewConversationsGoToTheDefaultSession(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	addAgent(t, st, "alice", nil)
	addAgent(t, st, "claude", nil)
	now := time.Now()
	report(t, b, "claude", "old", "old", bus.TierPush, now.Add(-time.Hour))
	report(t, b, "claude", "new", "new", bus.TierPush, now)
	report(t, b, "claude", "headless", "cli", bus.TierPull, now.Add(time.Second))

	m, err := b.Send(ctx, "alice", bus.SendInput{To: "claude", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Note, "Claude Code · new gets this right away") {
		t.Fatalf("note = %q", m.Note)
	}
	// A session that can't see it soon doesn't win just by being most recent.
	if got := take(t, b, "claude", "headless"); len(got) != 0 {
		t.Fatalf("pull session took %+v", got)
	}
	if got := take(t, b, "claude", "old"); len(got) != 0 {
		t.Fatalf("old session took %+v", got)
	}
	if got := take(t, b, "claude", "new"); len(got) != 1 {
		t.Fatalf("default session got %+v", got)
	}

	// A chosen default wins, by name, across restarts of that session.
	if err := st.SetAgentConfig(ctx, "claude", "default_session", "old"); err != nil {
		t.Fatal(err)
	}
	b.Send(ctx, "alice", bus.SendInput{To: "claude", Content: "again"})
	if got := take(t, b, "claude", "new"); len(got) != 0 {
		t.Fatalf("new session took %+v", got)
	}
	if got := take(t, b, "claude", "old"); len(got) != 1 {
		t.Fatalf("chosen session got %+v", got)
	}
}

func TestAnswersGoBackToTheSessionThatAsked(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	addAgent(t, st, "bot", nil)
	addAgent(t, st, "claude", nil)
	now := time.Now()
	report(t, b, "claude", "asker", "asker", bus.TierPush, now.Add(-time.Hour))
	report(t, b, "claude", "listener", "listener", bus.TierPush, now)

	q, err := b.Send(ctx, "claude", bus.SendInput{To: "bot", Content: "question", FromSession: "asker"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.Send(ctx, "bot", bus.SendInput{InReplyTo: q.ID, Content: "answer"})
	if err != nil || a.ToSession != "asker" {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	if got := take(t, b, "claude", "listener"); len(got) != 0 {
		t.Fatalf("the default session took another session's answer: %+v", got)
	}
	if got := take(t, b, "claude", "asker"); len(got) != 1 {
		t.Fatalf("asker got %+v", got)
	}

	// If the asker has gone, its answer falls to the default session.
	b.Send(ctx, "bot", bus.SendInput{InReplyTo: q.ID, Content: "one more thing"})
	if err := b.EndSession(ctx, "claude", "asker"); err != nil {
		t.Fatal(err)
	}
	if got := take(t, b, "claude", "listener"); len(got) != 1 {
		t.Fatalf("listener got %+v", got)
	}
}

func TestSessionsSayWhatTheyAreDoing(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	addAgent(t, st, "claude", nil)
	for id, in := range map[string]bus.SessionInput{
		"s1": {Name: "api", State: bus.StateWaiting, Branch: "fix/login"},
		"s2": {Name: "web", State: "dreaming", Branch: "bad\x1b[31m"},
	} {
		if err := b.ReportSession(ctx, "claude", id, in); err != nil {
			t.Fatal(err)
		}
	}
	agents, err := b.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bus.Session{}
	for _, s := range agents[0].Sessions {
		got[s.ID] = s
	}
	if s := got["s1"]; s.State != bus.StateWaiting || s.Branch != "fix/login" {
		t.Fatalf("s1 = %+v", s)
	}
	// What this version can't use is dropped, not refused.
	if s := got["s2"]; s.State != "" || s.Branch != "" {
		t.Fatalf("s2 = %+v", s)
	}
}

func TestClosedSessionsAreListedAfterRunningOnes(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	addAgent(t, st, "claude", nil)
	now := time.Now()
	report(t, b, "claude", "old", "old", bus.TierPush, now.Add(-time.Hour))
	report(t, b, "claude", "gone", "gone", bus.TierPush, now)
	report(t, b, "claude", "new", "new", bus.TierPush, now.Add(-time.Minute))
	if err := b.EndSession(ctx, "claude", "gone"); err != nil {
		t.Fatal(err)
	}

	a, _ := st.GetAgent(ctx, "claude")
	live, err := b.Sessions(ctx, a)
	if err != nil || len(live) != 2 {
		t.Fatalf("live = %+v, %v", live, err)
	}
	recent, err := b.RecentSessions(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range recent {
		order = append(order, s.ID)
	}
	if strings.Join(order, " ") != "new old gone" {
		t.Fatalf("order = %v", order)
	}
	if !recent[0].Default || recent[2].Live(time.Now()) || recent[2].EndedAt.IsZero() {
		t.Fatalf("recent = %+v", recent)
	}
}

func TestAddressingOneSession(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	addAgent(t, st, "bot", nil)
	addAgent(t, st, "claude", nil)
	now := time.Now()
	report(t, b, "claude", "s1", "api", bus.TierPush, now)
	report(t, b, "claude", "s2", "web", bus.TierPush, now.Add(-time.Minute))
	report(t, b, "claude", "s3", "web", bus.TierPush, now.Add(-2*time.Minute))

	m, err := b.Send(ctx, "bot", bus.SendInput{To: "claude/api", Content: "x"})
	if err != nil || m.To != "claude" || m.ToSession != "s1" {
		t.Fatalf("m = %+v, %v", m, err)
	}
	if _, err := b.Send(ctx, "bot", bus.SendInput{To: "claude/web", Content: "x"}); !errors.Is(err, bus.ErrInvalid) || !strings.Contains(err.Error(), "claude/s2") {
		t.Fatalf("ambiguous name: err = %v", err)
	}
	if _, err := b.Send(ctx, "bot", bus.SendInput{To: "claude/s3", Content: "x"}); err != nil {
		t.Fatalf("by id: %v", err)
	}
	if _, err := b.Send(ctx, "bot", bus.SendInput{To: "claude/none", Content: "x"}); !errors.Is(err, bus.ErrNotFound) {
		t.Fatalf("unknown session: err = %v", err)
	}
}

func TestClaimedMessagesComeBackUnlessConfirmed(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	addAgent(t, st, "bot", nil)
	addAgent(t, st, "claude", nil)
	report(t, b, "claude", "s1", "api", bus.TierPush, time.Now())
	claim := func(lease time.Duration) []bus.Message {
		msgs, err := b.ReceiveFor(ctx, bus.ReceiveInput{Agent: "claude", Session: "s1", Lease: lease})
		if err != nil {
			t.Fatal(err)
		}
		return msgs
	}
	m, _ := b.Send(ctx, "bot", bus.SendInput{To: "claude", Content: "x"})

	if got := claim(time.Millisecond); len(got) != 1 || got[0].Status != bus.StatusClaimed {
		t.Fatalf("first claim = %+v", got)
	}
	time.Sleep(5 * time.Millisecond)
	if got := claim(time.Minute); len(got) != 1 {
		t.Fatalf("an unconfirmed message should come back after its lease: %+v", got)
	}
	if got := claim(time.Minute); len(got) != 0 {
		t.Fatalf("a message under lease was taken twice: %+v", got)
	}
	if err := b.Nack(ctx, "claude", m.ID); err != nil {
		t.Fatal(err)
	}
	if got := claim(time.Minute); len(got) != 1 {
		t.Fatalf("a returned message should be available again: %+v", got)
	}
	if err := b.Ack(ctx, "claude", m.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetMessage(ctx, m.ID)
	if got.Status != bus.StatusDelivered {
		t.Fatalf("status = %q", got.Status)
	}
	if err := b.Nack(ctx, "claude", m.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMessage(ctx, m.ID); got.Status != bus.StatusDelivered {
		t.Fatalf("a confirmed message went back to %q", got.Status)
	}
	// Answering records that too, and only the recipient's answer counts.
	if _, err := b.Send(ctx, "bot", bus.SendInput{InReplyTo: m.ID, Content: "still there?"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMessage(ctx, m.ID); got.Status != bus.StatusDelivered {
		t.Fatalf("the sender's own follow-up made it %q", got.Status)
	}
	if _, err := b.Send(ctx, "claude", bus.SendInput{InReplyTo: m.ID, Content: "yes"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMessage(ctx, m.ID); got.Status != bus.StatusReplied {
		t.Fatalf("status after answering = %q", got.Status)
	}
}

func TestLoopLimit(t *testing.T) {
	ctx := context.Background()
	b, st := newBus(t)
	addAgent(t, st, "a", nil)
	addAgent(t, st, "b", nil)
	b.LoopLimit = 3
	for i := range 3 {
		if _, err := b.Send(ctx, "a", bus.SendInput{To: "b", Content: "x"}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := b.Send(ctx, "a", bus.SendInput{To: "b", Content: "x"}); !errors.Is(err, bus.ErrLoop) {
		t.Fatalf("err = %v, want ErrLoop", err)
	}
	if _, err := b.Send(ctx, "b", bus.SendInput{To: "a", Content: "x"}); err != nil {
		t.Fatalf("the other direction has its own count: %v", err)
	}
	b.LoopLimit = -1
	if _, err := b.Send(ctx, "a", bus.SendInput{To: "b", Content: "x"}); err != nil {
		t.Fatalf("limit off: %v", err)
	}
}

type fakeDeliverer struct {
	kind string
	err  error
	got  chan bus.Message
}

func (f *fakeDeliverer) Kind() string { return f.kind }
func (f *fakeDeliverer) Deliver(_ context.Context, _ bus.Agent, m bus.Message) error {
	f.got <- m
	return f.err
}

func waitStatus(t *testing.T, st interface {
	GetMessage(context.Context, string) (bus.Message, error)
}, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m, _ := st.GetMessage(context.Background(), id)
		if m.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %q, want %q", m.Status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSenderHearsWhenDeliveryGivesUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &fakeDeliverer{kind: "webhook", err: errors.New("HTTP 500"), got: make(chan bus.Message, 1)}
	b, st := newBus(t, d)
	addAgent(t, st, "alice", nil)
	addAgent(t, st, "down", map[string]string{"url": "http://x"})
	report(t, b, "alice", "s1", "api", bus.TierPush, time.Now())

	m, err := b.Send(ctx, "alice", bus.SendInput{To: "down", Content: "x", FromSession: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	// Seven failures already, so the next attempt is the last.
	for range 7 {
		if err := st.RecordFailure(ctx, m.ID, "HTTP 500", time.Time{}, false); err != nil {
			t.Fatal(err)
		}
	}
	go b.RunDelivery(ctx)
	got, err := b.Receive(ctx, "alice", m.ThreadID, 10*time.Second)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	n := got[0]
	if n.From != bus.SystemSender || n.InReplyTo != m.ID || n.ToSession != "s1" || !strings.Contains(n.Content, "HTTP 500") {
		t.Fatalf("notice = %+v", n)
	}
	if _, err := b.Send(ctx, "alice", bus.SendInput{InReplyTo: n.ID, Content: "thanks"}); !errors.Is(err, bus.ErrInvalid) {
		t.Fatalf("answering a notice: err = %v", err)
	}
}

func TestAcceptedWorkRunsAgainAfterRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &fakeDeliverer{kind: "webhook", err: bus.ErrAccepted, got: make(chan bus.Message, 2)}
	b, st := newBus(t, d)
	addAgent(t, st, "alice", nil)
	addAgent(t, st, "slow", map[string]string{"url": "http://x"})
	m, err := b.Send(ctx, "alice", bus.SendInput{To: "slow", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { b.RunDelivery(ctx); close(done) }()
	<-d.got
	waitStatus(t, st, m.ID, bus.StatusClaimed)
	cancel()
	<-done

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go b.RunDelivery(ctx)
	select {
	case again := <-d.got:
		if again.ID != m.ID {
			t.Fatalf("redelivered %s", again.ID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("unfinished work wasn't picked up after the restart")
	}
	waitStatus(t, st, m.ID, bus.StatusClaimed)
	if err := b.Complete(ctx, "slow", m.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, m.ID, bus.StatusDelivered)
}
