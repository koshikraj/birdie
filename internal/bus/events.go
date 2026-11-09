package bus

import (
	"sync"
	"time"
)

// Event is something that happened on the bus, for watchers such as the web
// app. Kind is "message" or "session".
type Event struct {
	Kind string    `json:"kind"`
	Type string    `json:"type"`
	At   time.Time `json:"at"`

	// For a message: its id, thread, ends, the session that took it, the
	// start of its text, and why a try failed. Type is queued, claimed,
	// delivered, replied, retrying or failed.
	ID          string `json:"id,omitempty"`
	ThreadID    string `json:"thread_id,omitempty"`
	InReplyTo   string `json:"in_reply_to,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	FromSession string `json:"from_session,omitempty"`
	ToSession   string `json:"to_session,omitempty"`
	TakenBy     string `json:"taken_by,omitempty"`
	Text        string `json:"text,omitempty"`
	Error       string `json:"error,omitempty"`

	// For a session: whose it is, its id, name and state. Type is reported
	// or ended.
	Agent   string `json:"agent,omitempty"`
	Session string `json:"session,omitempty"`
	Name    string `json:"name,omitempty"`
	State   string `json:"state,omitempty"`
}

const (
	eventBuffer  = 64
	previewRunes = 200
)

type watchers struct {
	mu    sync.Mutex
	chans map[chan Event]struct{}
}

// Watch returns bus events as they happen, and a function that stops them.
// A watcher that falls behind misses events rather than slowing the bus.
func (b *Bus) Watch() (<-chan Event, func()) {
	ch := make(chan Event, eventBuffer)
	b.watch.mu.Lock()
	if b.watch.chans == nil {
		b.watch.chans = make(map[chan Event]struct{})
	}
	b.watch.chans[ch] = struct{}{}
	b.watch.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.watch.mu.Lock()
			delete(b.watch.chans, ch)
			b.watch.mu.Unlock()
		})
	}
}

func (b *Bus) emit(e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b.watch.mu.Lock()
	defer b.watch.mu.Unlock()
	for ch := range b.watch.chans {
		select {
		case ch <- e:
		default:
		}
	}
}

func (b *Bus) emitMessage(typ string, m Message, takenBy, errMsg string) {
	text := []rune(m.Content)
	if len(text) > previewRunes {
		text = text[:previewRunes]
	}
	b.emit(Event{
		Kind: "message", Type: typ, ID: m.ID, ThreadID: m.ThreadID, InReplyTo: m.InReplyTo,
		From: m.From, To: m.To, FromSession: m.FromSession, ToSession: m.ToSession,
		TakenBy: takenBy, Text: string(text), Error: errMsg,
	})
}
