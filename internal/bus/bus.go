package bus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	MaxContentBytes = 1 << 20
	MaxWait         = 2 * time.Minute
	inboxBatch      = 50
	maxAttempts     = 8
	deliverTimeout  = 30 * time.Second
	sweepInterval   = 5 * time.Second

	// SessionTTL is how long a session counts as running after it last
	// reported. Sessions report every SessionBeat.
	SessionTTL  = 90 * time.Second
	SessionBeat = 30 * time.Second
	// SessionRetention is how long a session is remembered after it stops.
	SessionRetention = 24 * time.Hour
	maxBranchBytes   = 255

	// DefaultLoopLimit messages from one agent to another within LoopWindow
	// probably means they are stuck answering each other.
	DefaultLoopLimit = 12
	LoopWindow       = 10 * time.Minute

	noticeKey = "notice"
)

type Bus struct {
	store      Store
	deliverers map[string]Deliverer
	log        *slog.Logger
	// LoopLimit overrides DefaultLoopLimit; negative turns the check off.
	LoopLimit int

	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
	kick    chan struct{}
	watch   watchers
}

func New(s Store, log *slog.Logger, ds ...Deliverer) *Bus {
	b := &Bus{
		store:      s,
		deliverers: make(map[string]Deliverer),
		log:        log,
		waiters:    make(map[string]map[chan struct{}]struct{}),
		kick:       make(chan struct{}, 1),
	}
	for _, d := range ds {
		b.deliverers[d.Kind()] = d
	}
	return b
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func NewToken() string { return "ab_" + randHex(32) }

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (b *Bus) Authenticate(ctx context.Context, token string) (Agent, error) {
	if token == "" {
		return Agent{}, ErrUnauthorized
	}
	a, err := b.store.AgentByTokenHash(ctx, HashToken(token))
	if errors.Is(err, ErrNotFound) {
		return Agent{}, ErrUnauthorized
	}
	return a, err
}

// Agents lists every agent with its running sessions.
func (b *Bus) Agents(ctx context.Context) ([]Agent, error) {
	return b.listAgents(ctx, b.Sessions)
}

// AgentsWithRecentSessions lists every agent with its sessions from the last
// day, as RecentSessions returns them.
func (b *Bus) AgentsWithRecentSessions(ctx context.Context) ([]Agent, error) {
	return b.listAgents(ctx, b.RecentSessions)
}

func (b *Bus) listAgents(ctx context.Context, sessions func(context.Context, Agent) ([]Session, error)) ([]Agent, error) {
	agents, err := b.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for i := range agents {
		if agents[i].Delivery != DeliveryPull {
			continue
		}
		if agents[i].Sessions, err = sessions(ctx, agents[i]); err != nil {
			return nil, err
		}
	}
	return agents, nil
}

// Sessions returns the agent's running sessions, with its default marked.
func (b *Bus) Sessions(ctx context.Context, a Agent) ([]Session, error) {
	live, err := b.store.LiveSessions(ctx, a.Name, time.Now().Add(-SessionTTL))
	if err != nil {
		return nil, err
	}
	if d := defaultSession(live, a.Config["default_session"]); d != nil {
		d.Default = true
	}
	return live, nil
}

// RecentSessions returns the agent's sessions from the last day: the running
// ones as Sessions returns them, then the ones that stopped, latest first.
func (b *Bus) RecentSessions(ctx context.Context, a Agent) ([]Session, error) {
	now := time.Now()
	all, err := b.store.RecentSessions(ctx, a.Name, now.Add(-SessionRetention))
	if err != nil {
		return nil, err
	}
	var live, stopped []Session
	for _, s := range all {
		if s.Live(now) {
			live = append(live, s)
		} else {
			stopped = append(stopped, s)
		}
	}
	if d := defaultSession(live, a.Config["default_session"]); d != nil {
		d.Default = true
	}
	slices.SortStableFunc(stopped, func(x, y Session) int { return y.StoppedAt().Compare(x.StoppedAt()) })
	return append(live, stopped...), nil
}

// defaultSession picks the session that takes new conversations: the one with
// the name the user chose, else the one that can see messages soonest, most
// recently active first. It returns nil if there are none.
func defaultSession(live []Session, chosen string) *Session {
	var best *Session
	for i := range live {
		s := &live[i]
		if chosen != "" && s.Name == chosen {
			return s
		}
		if best == nil || tierRank(s.Tier) > tierRank(best.Tier) ||
			(tierRank(s.Tier) == tierRank(best.Tier) && s.ActiveAt.After(best.ActiveAt)) {
			best = s
		}
	}
	return best
}

func tierRank(tier string) int {
	switch tier {
	case TierPush:
		return 2
	case TierTurn:
		return 1
	}
	return 0
}

// ReportSession records that a session is running.
func (b *Bus) ReportSession(ctx context.Context, agent, id string, in SessionInput) error {
	if !sessionPattern.MatchString(id) {
		return fmt.Errorf("%w: bad session id %q", ErrInvalid, id)
	}
	if in.Name != "" && !sessionPattern.MatchString(in.Name) {
		return fmt.Errorf("%w: bad session name %q", ErrInvalid, in.Name)
	}
	switch in.Tier {
	case TierPush, TierTurn, TierPull:
	case "":
		in.Tier = TierPull
	default:
		return fmt.Errorf("%w: bad tier %q", ErrInvalid, in.Tier)
	}
	// What a session says about itself only informs people, so anything this
	// version can't use is dropped rather than refused.
	switch in.State {
	case StateBusy, StateIdle, StateWaiting:
	default:
		in.State = ""
	}
	if len(in.Branch) > maxBranchBytes || strings.ContainsFunc(in.Branch, unicode.IsControl) {
		in.Branch = ""
	}
	now := time.Now().UTC()
	s := Session{ID: id, Agent: agent, Name: in.Name, Cwd: in.Cwd, Branch: in.Branch, Tier: in.Tier, State: in.State, ActiveAt: in.ActiveAt, LastSeen: now}
	if s.ActiveAt.IsZero() || s.ActiveAt.After(now) {
		s.ActiveAt = now
	}
	if err := b.store.UpsertSession(ctx, s); err != nil {
		return err
	}
	b.notify(agent)
	b.emit(Event{Kind: "session", Type: "reported", Agent: agent, Session: id, Name: s.Name, State: s.State})
	return nil
}

// EndSession records that a session closed, so its waiting messages move on
// without delay.
func (b *Bus) EndSession(ctx context.Context, agent, id string) error {
	if err := b.store.EndSession(ctx, agent, id, time.Now().UTC()); err != nil {
		return err
	}
	b.notify(agent)
	b.emit(Event{Kind: "session", Type: "ended", Agent: agent, Session: id})
	return nil
}

// Send queues a message from the authenticated agent from. Replies inherit
// the parent's thread (and its other participant when To is empty), and only
// participants may post into an existing thread.
func (b *Bus) Send(ctx context.Context, from string, in SendInput) (Message, error) {
	if strings.TrimSpace(in.Content) == "" {
		return Message{}, fmt.Errorf("%w: content is required", ErrInvalid)
	}
	if len(in.Content) > MaxContentBytes {
		return Message{}, fmt.Errorf("%w: content exceeds %d bytes", ErrInvalid, MaxContentBytes)
	}

	thread := in.ThreadID
	var pinned string // the session the thread says should get this
	var parent Message
	if in.InReplyTo != "" {
		var err error
		parent, err = b.store.GetMessage(ctx, in.InReplyTo)
		if errors.Is(err, ErrNotFound) {
			return Message{}, fmt.Errorf("%w: unknown message %q", ErrNotFound, in.InReplyTo)
		} else if err != nil {
			return Message{}, err
		}
		if parent.From != from && parent.To != from {
			return Message{}, fmt.Errorf("%w: not a participant in message %q", ErrForbidden, in.InReplyTo)
		}
		if thread != "" && thread != parent.ThreadID {
			return Message{}, fmt.Errorf("%w: thread_id does not match in_reply_to", ErrInvalid)
		}
		thread = parent.ThreadID
		if in.To == "" {
			in.To = parent.From
			if parent.From == from {
				in.To = parent.To
			}
		}
		// An answer goes back to the session that asked.
		if in.To == parent.From {
			pinned = parent.FromSession
		} else if in.To == parent.To {
			pinned = parent.ToSession
		}
	} else if thread != "" {
		exists, member, err := b.store.ThreadAccess(ctx, thread, from)
		if err != nil {
			return Message{}, err
		}
		if exists && !member {
			return Message{}, fmt.Errorf("%w: not a participant in thread %q", ErrForbidden, thread)
		}
	}
	if in.To == "" {
		return Message{}, fmt.Errorf("%w: to is required unless in_reply_to is set", ErrInvalid)
	}
	if IsSystemSender(in.To) {
		return Message{}, fmt.Errorf("%w: %s notices can't be answered", ErrInvalid, SystemSender)
	}
	addr, err := ParseAddress(in.To)
	if err != nil {
		return Message{}, err
	}
	if addr.Node != "" {
		return Message{}, fmt.Errorf("%w: can't reach %q: birdie only connects agents on this machine for now", ErrInvalid, in.To)
	}
	to, err := b.store.GetAgent(ctx, addr.Agent)
	if errors.Is(err, ErrNotFound) {
		return Message{}, fmt.Errorf("%w: unknown agent %q", ErrNotFound, addr.Agent)
	} else if err != nil {
		return Message{}, err
	}
	if err := b.admit(ctx, from, to.Name); err != nil {
		return Message{}, err
	}
	toSession := pinned
	if addr.Session != "" {
		s, err := b.findSession(ctx, to, addr.Session)
		if err != nil {
			return Message{}, err
		}
		toSession = s.ID
	}

	id := randHex(12)
	if thread == "" {
		thread = "thr_" + id
	}
	m := Message{
		ID:          "msg_" + id,
		ThreadID:    thread,
		InReplyTo:   in.InReplyTo,
		From:        from,
		To:          to.Name,
		FromSession: in.FromSession,
		ToSession:   toSession,
		Content:     in.Content,
		Meta:        in.Meta,
		Status:      StatusQueued,
		CreatedAt:   time.Now().UTC(),
	}
	if err := b.store.InsertMessage(ctx, m); err != nil {
		return Message{}, err
	}
	b.emitMessage(StatusQueued, m, "", "")
	if parent.ID != "" {
		if err := b.store.MarkReplied(ctx, parent.ID, from); err != nil {
			b.log.Error("mark replied", "id", parent.ID, "err", err)
		} else if parent.To == from && parent.Status != StatusFailed {
			b.emitMessage(StatusReplied, parent, "", "")
		}
	}
	m.Note = b.deliveryNote(ctx, to, m)
	if to.Delivery == DeliveryPull {
		b.notify(to.Name)
	} else {
		if _, ok := b.deliverers[to.Delivery]; !ok {
			b.log.Warn("no deliverer registered; message stays queued", "kind", to.Delivery, "to", to.Name)
		}
		select {
		case b.kick <- struct{}{}:
		default:
		}
	}
	return m, nil
}

// Receive claims queued messages for agent, long-polling up to wait when the
// inbox is empty.
func (b *Bus) Receive(ctx context.Context, agent, threadID string, wait time.Duration) ([]Message, error) {
	return b.ReceiveFor(ctx, ReceiveInput{Agent: agent, Thread: threadID, Wait: wait})
}

// ReceiveFor is Receive with session scoping and leases.
func (b *Bus) ReceiveFor(ctx context.Context, in ReceiveInput) ([]Message, error) {
	deadline := time.Now().Add(min(in.Wait, MaxWait))
	for {
		// Subscribe before claiming so a send between the two isn't missed.
		ch := b.subscribe(in.Agent)
		f, err := b.claimFilter(ctx, in)
		var msgs []Message
		if err == nil {
			msgs, err = b.store.ClaimInbox(ctx, f, time.Now())
		}
		if err != nil || len(msgs) > 0 {
			b.unsubscribe(in.Agent, ch)
			if err == nil {
				for _, m := range msgs {
					b.emitMessage(m.Status, m, in.Session, "")
				}
			}
			return msgs, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			b.unsubscribe(in.Agent, ch)
			return []Message{}, nil
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ch:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			b.unsubscribe(in.Agent, ch)
			return nil, ctx.Err()
		}
		timer.Stop()
		b.unsubscribe(in.Agent, ch)
	}
}

// Unread counts what the receiver would get from ReceiveFor right now.
func (b *Bus) Unread(ctx context.Context, in ReceiveInput) (int, error) {
	f, err := b.claimFilter(ctx, in)
	if err != nil {
		return 0, err
	}
	return b.store.CountInbox(ctx, f, time.Now())
}

func (b *Bus) claimFilter(ctx context.Context, in ReceiveInput) (ClaimFilter, error) {
	now := time.Now()
	f := ClaimFilter{Agent: in.Agent, Thread: in.Thread, Session: in.Session, LiveSince: now.Add(-SessionTTL), Limit: inboxBatch}
	if in.Lease > 0 {
		f.LeaseUntil = now.Add(in.Lease)
	}
	if in.Session == "" {
		return f, nil
	}
	// Waiting on a thread names what it wants, so it doesn't need to be the
	// default session. Otherwise only the default takes new conversations.
	if in.Thread != "" {
		f.Unpinned = true
		return f, nil
	}
	a, err := b.store.GetAgent(ctx, in.Agent)
	if err != nil {
		return f, err
	}
	live, err := b.store.LiveSessions(ctx, in.Agent, f.LiveSince)
	if err != nil {
		return f, err
	}
	d := defaultSession(live, a.Config["default_session"])
	f.Unpinned = d != nil && d.ID == in.Session
	return f, nil
}

// Ack confirms the agent got a claimed message.
func (b *Bus) Ack(ctx context.Context, agent, id string) error {
	changed, err := b.store.Settle(ctx, id, agent, true, time.Now())
	if changed {
		if m, err := b.store.GetMessage(ctx, id); err == nil {
			b.emitMessage(StatusDelivered, m, "", "")
		}
	}
	return err
}

// Nack returns a claimed message to the queue because it couldn't be shown.
func (b *Bus) Nack(ctx context.Context, agent, id string) error {
	changed, err := b.store.Settle(ctx, id, agent, false, time.Now())
	if changed {
		b.notify(agent)
	}
	return err
}

// Complete finishes a message a Deliverer accepted with ErrAccepted.
func (b *Bus) Complete(ctx context.Context, agent, id string) error {
	return b.Ack(ctx, agent, id)
}

// Notice tells the sender of m something went wrong with it. kind keeps it to
// one notice of that kind per thread.
func (b *Bus) Notice(ctx context.Context, m Message, kind, text string) error {
	if IsSystemSender(m.From) {
		return nil
	}
	if msgs, err := b.store.Thread(ctx, m.ThreadID, m.From); err == nil {
		for _, old := range msgs {
			if IsSystemSender(old.From) && old.Meta[noticeKey] == kind {
				return nil
			}
		}
	}
	id := randHex(12)
	n := Message{
		ID:        "msg_" + id,
		ThreadID:  m.ThreadID,
		InReplyTo: m.ID,
		From:      SystemSender,
		To:        m.From,
		ToSession: m.FromSession,
		Content:   text,
		Meta:      map[string]string{noticeKey: kind},
		Status:    StatusQueued,
		CreatedAt: time.Now().UTC(),
	}
	if err := b.store.InsertMessage(ctx, n); err != nil {
		return err
	}
	b.emitMessage(StatusQueued, n, "", "")
	b.notify(m.From)
	select {
	case b.kick <- struct{}{}:
	default:
	}
	return nil
}

// admit decides whether from may message to right now. Rules about who may
// reach whom belong here, including any later ones for other machines.
func (b *Bus) admit(ctx context.Context, from, to string) error {
	limit := b.LoopLimit
	if limit == 0 {
		limit = DefaultLoopLimit
	}
	if limit < 0 {
		return nil
	}
	n, err := b.store.CountSent(ctx, from, to, time.Now().Add(-LoopWindow))
	if err != nil {
		return err
	}
	if n >= limit {
		return fmt.Errorf("%w: %s has sent %s %d messages in the last %v, which looks like a loop; check with your user before continuing",
			ErrLoop, from, to, n, LoopWindow)
	}
	return nil
}

func (b *Bus) findSession(ctx context.Context, to Agent, want string) (Session, error) {
	live, err := b.store.LiveSessions(ctx, to.Name, time.Now().Add(-SessionTTL))
	if err != nil {
		return Session{}, err
	}
	var match []Session
	for _, s := range live {
		if s.ID == want {
			return s, nil
		}
		if s.Name == want {
			match = append(match, s)
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return Session{}, fmt.Errorf("%w: %s has no running session %q", ErrNotFound, to.Name, want)
	}
	ids := make([]string, len(match))
	for i, s := range match {
		ids[i] = to.Name + "/" + s.ID
	}
	return Session{}, fmt.Errorf("%w: %s has %d sessions named %q; use one of %s", ErrInvalid, to.Name, len(match), want, strings.Join(ids, ", "))
}

// deliveryNote tells the sender when the recipient will see m, naming it by
// its label.
func (b *Bus) deliveryNote(ctx context.Context, to Agent, m Message) string {
	switch to.Delivery {
	case DeliveryPull:
	case "webhook":
		return fmt.Sprintf("queued: being sent to %s's webhook; it answers in its own time", to.DisplayName())
	case "acp":
		return fmt.Sprintf("queued: birdie will start %s for this and post its answer on this thread", to.DisplayName())
	default:
		return "queued"
	}
	live, err := b.Sessions(ctx, to)
	if err != nil {
		return "queued"
	}
	var target *Session
	for i := range live {
		if (m.ToSession != "" && live[i].ID == m.ToSession) || (m.ToSession == "" && live[i].Default) {
			target = &live[i]
		}
	}
	if target == nil {
		if m.ToSession != "" {
			for i := range live {
				if live[i].Default {
					target = &live[i]
				}
			}
		}
		if target == nil {
			return fmt.Sprintf("queued: %s has no open session; it gets this when it next checks or starts", to.DisplayName())
		}
	}
	name := to.DisplayName()
	if target.Name != "" {
		name += " · " + target.Name
	}
	switch target.Tier {
	case TierPush:
		return fmt.Sprintf("queued: %s gets this right away", name)
	case TierTurn:
		return fmt.Sprintf("queued: %s sees this at its next tool call or end of turn; if it's idle, it waits for its user", name)
	}
	return fmt.Sprintf("queued: %s only sees this when it checks its inbox", name)
}

func (b *Bus) Thread(ctx context.Context, threadID, agent string) ([]Message, error) {
	msgs, err := b.store.Thread(ctx, threadID, agent)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("%w: thread %q", ErrNotFound, threadID)
	}
	return msgs, nil
}

func (b *Bus) subscribe(agent string) chan struct{} {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if b.waiters[agent] == nil {
		b.waiters[agent] = make(map[chan struct{}]struct{})
	}
	b.waiters[agent][ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Bus) unsubscribe(agent string, ch chan struct{}) {
	b.mu.Lock()
	delete(b.waiters[agent], ch)
	if len(b.waiters[agent]) == 0 {
		delete(b.waiters, agent)
	}
	b.mu.Unlock()
}

func (b *Bus) notify(agent string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.waiters[agent] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// RunDelivery pushes queued messages to non-pull agents until ctx ends,
// retrying failures with exponential backoff.
func (b *Bus) RunDelivery(ctx context.Context) {
	if err := b.store.ReleaseOutbound(ctx); err != nil {
		b.log.Error("release unfinished deliveries", "err", err)
	}
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		b.deliverDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-b.kick:
		}
	}
}

func (b *Bus) deliverDue(ctx context.Context) {
	due, err := b.store.DueOutbound(ctx, time.Now(), inboxBatch)
	if err != nil {
		if ctx.Err() == nil {
			b.log.Error("load outbound", "err", err)
		}
		return
	}
	for _, o := range due {
		err := fmt.Errorf("no deliverer for kind %q", o.Agent.Delivery)
		if d, ok := b.deliverers[o.Agent.Delivery]; ok {
			dctx, cancel := context.WithTimeout(ctx, deliverTimeout)
			err = d.Deliver(dctx, o.Agent, o.Message)
			cancel()
		}
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, ErrAccepted) {
			if err := b.store.MarkClaimed(ctx, o.Message.ID); err != nil {
				b.log.Error("mark claimed", "id", o.Message.ID, "err", err)
			} else {
				b.emitMessage(StatusClaimed, o.Message, "", "")
			}
			continue
		}
		if err == nil {
			if err := b.store.MarkDelivered(ctx, o.Message.ID, time.Now()); err != nil {
				b.log.Error("mark delivered", "id", o.Message.ID, "err", err)
			} else {
				b.emitMessage(StatusDelivered, o.Message, "", "")
			}
			continue
		}
		attempts := o.Message.Attempts + 1
		failed := attempts >= maxAttempts
		next := time.Now().Add(min(time.Duration(1<<attempts)*time.Second, 5*time.Minute))
		b.log.Warn("delivery failed", "id", o.Message.ID, "to", o.Agent.Name, "attempt", attempts, "gave_up", failed, "err", err)
		why := err.Error()
		if err := b.store.RecordFailure(ctx, o.Message.ID, why, next, failed); err != nil {
			b.log.Error("record failure", "id", o.Message.ID, "err", err)
		} else if failed {
			b.emitMessage(StatusFailed, o.Message, "", why)
		} else {
			b.emitMessage("retrying", o.Message, "", why)
		}
		if failed {
			text := fmt.Sprintf("Your message %s to %s could not be delivered after %d attempts: %v", o.Message.ID, o.Agent.Name, attempts, err)
			if err := b.Notice(ctx, o.Message, "delivery_failed", text); err != nil {
				b.log.Error("send notice", "id", o.Message.ID, "err", err)
			}
		}
	}
}
