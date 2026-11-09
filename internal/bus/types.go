// Package bus is the protocol-neutral core of birdie: agents, messages,
// threads, inboxes and outbound delivery. Every transport (HTTP, MCP, A2A,
// ACP, webhooks) is a thin adapter over this package.
package bus

import (
	"context"
	"errors"
	"regexp"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalid      = errors.New("invalid request")
	// ErrLoop refuses a message because two agents look stuck in a loop.
	ErrLoop = errors.New("too many messages")
	// ErrAccepted is returned by a Deliverer that has taken the message and
	// will finish later, by calling Bus.Complete. It isn't a failure.
	ErrAccepted = errors.New("accepted")
)

const (
	StatusQueued = "queued"
	// StatusClaimed means a receiver has the message but hasn't confirmed the
	// agent got it. If it never confirms, the message is delivered again.
	StatusClaimed   = "claimed"
	StatusDelivered = "delivered"
	StatusReplied   = "replied"
	StatusFailed    = "failed"

	// DeliveryPull agents fetch their own inbox (MCP, HTTP long-poll).
	// Any other delivery kind is pushed by the Deliverer registered for it.
	DeliveryPull = "pull"

	// SystemSender is the sender of notices the bus itself writes. It is not
	// an agent, can't be registered, and can't be answered.
	SystemSender = "birdie"
	// LegacySystemSender signed notices sent under the project's old name.
	LegacySystemSender = "agent-bus"

	// How soon a session sees a message.
	TierPush = "push" // at once, even if idle
	TierTurn = "turn" // at its next tool call or end of turn
	TierPull = "pull" // only when it checks

	// What a session is doing, when its agent says. Most agents don't, and
	// their sessions have no state.
	StateBusy    = "busy"    // working on a turn
	StateIdle    = "idle"    // finished, until its user types again
	StateWaiting = "waiting" // stopped mid-turn until its user answers, as at a permission prompt
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidName reports whether name is usable as an agent name. Names double as
// token file names, so the pattern is deliberately strict.
// IsSystemSender says whether a sender name means the bus itself.
func IsSystemSender(name string) bool { return name == SystemSender || name == LegacySystemSender }

func ValidName(name string) bool { return namePattern.MatchString(name) && !IsSystemSender(name) }

type Agent struct {
	Name string `json:"name"`
	// Description tells other agents what this agent is for. It is kept in
	// Config["description"].
	Description string            `json:"description,omitempty"`
	TokenHash   string            `json:"-"`
	Delivery    string            `json:"delivery"`
	Config      map[string]string `json:"-"`
	CreatedAt   time.Time         `json:"created_at"`
	// Sessions lists the agent's running sessions, when it has any.
	Sessions []Session `json:"sessions,omitempty"`
}

// Session is one running conversation of an agent, such as one open Claude
// Code window. All sessions of an agent share its name and token.
type Session struct {
	ID    string `json:"id"`
	Agent string `json:"agent"`
	// Name is how other agents address it: agent/name.
	Name string `json:"name,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
	// Branch is the git branch checked out in Cwd, or the commit when no
	// branch is.
	Branch string `json:"branch,omitempty"`
	Tier   string `json:"tier"`
	// State is StateBusy, StateIdle or StateWaiting, or empty when its agent
	// doesn't say.
	State string `json:"state,omitempty"`
	// Default marks the session that takes new conversations.
	Default bool `json:"default,omitempty"`
	// ActiveAt is when its user or model last did something. While it is idle
	// or waiting, that is when it stopped.
	ActiveAt time.Time `json:"active_at"`
	LastSeen time.Time `json:"last_seen"`
}

// SessionInput is what a session reports about itself.
type SessionInput struct {
	Name     string    `json:"name,omitempty"`
	Cwd      string    `json:"cwd,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	Tier     string    `json:"tier,omitempty"`
	State    string    `json:"state,omitempty"`
	ActiveAt time.Time `json:"active_at,omitzero"`
}

type Message struct {
	ID        string `json:"id"`
	ThreadID  string `json:"thread_id"`
	InReplyTo string `json:"in_reply_to,omitempty"`
	From      string `json:"from"`
	To        string `json:"to"`
	// FromSession and ToSession pin the message to sessions of those agents.
	// An empty ToSession means whichever session takes new conversations.
	FromSession string            `json:"from_session,omitempty"`
	ToSession   string            `json:"to_session,omitempty"`
	Content     string            `json:"content"`
	Meta        map[string]string `json:"meta,omitempty"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	// Note says when the recipient will see the message. Only set on the
	// result of a send.
	Note string `json:"note,omitempty"`

	Attempts int `json:"-"`
}

type SendInput struct {
	To        string            `json:"to"`
	Content   string            `json:"content"`
	ThreadID  string            `json:"thread_id,omitempty"`
	InReplyTo string            `json:"in_reply_to,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
	// FromSession is the sending session, so an answer can come back to it.
	FromSession string `json:"from_session,omitempty"`
}

// ReceiveInput selects which messages a receiver takes.
type ReceiveInput struct {
	Agent  string
	Thread string
	Wait   time.Duration
	// Session scopes the inbox to one session: messages pinned to it, plus new
	// conversations if it is the agent's default session. Empty takes all.
	Session string
	// Lease, when set, keeps claimed messages for the receiver only this long
	// unless it confirms them with Ack. Without it they count as delivered at once.
	Lease time.Duration
}

// ClaimFilter is ReceiveInput as the store needs it.
type ClaimFilter struct {
	Agent, Thread, Session string
	// Unpinned also takes messages not pinned to a live session.
	Unpinned  bool
	LiveSince time.Time
	Limit     int
	// LeaseUntil, when set, claims rather than delivers.
	LeaseUntil time.Time
}

// Outbound is a queued message for a push-delivered agent.
type Outbound struct {
	Message Message
	Agent   Agent
}

type Store interface {
	CreateAgent(ctx context.Context, a Agent) error
	DeleteAgent(ctx context.Context, name string) error
	GetAgent(ctx context.Context, name string) (Agent, error)
	AgentByTokenHash(ctx context.Context, hash string) (Agent, error)
	ListAgents(ctx context.Context) ([]Agent, error)

	InsertMessage(ctx context.Context, m Message) error
	GetMessage(ctx context.Context, id string) (Message, error)
	// ThreadAccess reports whether the thread has any messages and whether
	// agent sent or received one of them.
	ThreadAccess(ctx context.Context, threadID, agent string) (exists, member bool, err error)
	// Thread returns the thread's messages that agent sent or received.
	Thread(ctx context.Context, threadID, agent string) ([]Message, error)
	// ClaimInbox takes up to f.Limit messages for f.Agent, oldest first:
	// queued ones and claimed ones whose lease ran out.
	ClaimInbox(ctx context.Context, f ClaimFilter, now time.Time) ([]Message, error)
	// CountInbox counts what ClaimInbox would take.
	CountInbox(ctx context.Context, f ClaimFilter, now time.Time) (int, error)
	// CountSent counts messages from one agent to another since a time.
	CountSent(ctx context.Context, from, to string, since time.Time) (int, error)

	DueOutbound(ctx context.Context, now time.Time, limit int) ([]Outbound, error)
	MarkDelivered(ctx context.Context, id string, at time.Time) error
	RecordFailure(ctx context.Context, id, errMsg string, next time.Time, failed bool) error
	// MarkClaimed records that a Deliverer is working on the message.
	MarkClaimed(ctx context.Context, id string) error
	// Settle moves a claimed message addressed to agent to delivered (ok) or
	// back to queued. It reports whether anything changed.
	Settle(ctx context.Context, id, agent string, ok bool, at time.Time) (bool, error)
	// MarkReplied records that agent answered the message.
	MarkReplied(ctx context.Context, id, agent string) error
	// ReleaseOutbound requeues work Deliverers had in progress when the bus
	// stopped.
	ReleaseOutbound(ctx context.Context) error

	UpsertSession(ctx context.Context, s Session) error
	DeleteSession(ctx context.Context, agent, id string) error
	// LiveSessions returns the agent's sessions seen since a time.
	LiveSessions(ctx context.Context, agent string, since time.Time) ([]Session, error)

	Close() error
}

// Deliverer pushes messages to agents whose Delivery equals Kind(). This is
// the outbound glue point for webhooks, A2A clients, ACP clients and so on.
type Deliverer interface {
	Kind() string
	Deliver(ctx context.Context, to Agent, m Message) error
}
