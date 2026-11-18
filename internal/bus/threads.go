package bus

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Thread is a conversation's own record: its title, who started it, and who
// last renamed it. Its messages carry its id.
type Thread struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	RenamedBy string    `json:"renamed_by,omitempty"`
	RenamedAt time.Time `json:"renamed_at,omitzero"`
}

const (
	// MaxTitleRunes caps a title someone gives a thread.
	MaxTitleRunes = 100
	// defaultTitleRunes caps the title taken from a thread's first message.
	defaultTitleRunes = 60
)

// CleanTitle makes s a title: one line, spaces collapsed, at most max runes,
// cut at a word where it can be, with an ellipsis when cut.
func CleanTitle(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)[:max-1]
	cut := string(r)
	if i := strings.LastIndexByte(cut, ' '); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-") + "…"
}

// TitleFrom is the title a thread gets from its first message: the first line
// that has words on it.
func TitleFrom(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if t := CleanTitle(strings.TrimLeft(line, "#>*-• \t"), defaultTitleRunes); t != "" {
			return t
		}
	}
	return ""
}

// ThreadInfo returns the thread's record, for a member of it.
func (b *Bus) ThreadInfo(ctx context.Context, threadID, agent string) (Thread, error) {
	exists, member, err := b.store.ThreadAccess(ctx, threadID, agent)
	if err != nil {
		return Thread{}, err
	}
	if !exists || !member {
		return Thread{}, fmt.Errorf("%w: thread %q", ErrNotFound, threadID)
	}
	return b.store.GetThread(ctx, threadID)
}

// RenameThread gives a thread a new title. Any member may.
func (b *Bus) RenameThread(ctx context.Context, threadID, agent, title string) (Thread, error) {
	title = CleanTitle(title, MaxTitleRunes)
	if title == "" {
		return Thread{}, fmt.Errorf("%w: a title needs some words", ErrInvalid)
	}
	exists, member, err := b.store.ThreadAccess(ctx, threadID, agent)
	if err != nil {
		return Thread{}, err
	}
	if !exists || !member {
		return Thread{}, fmt.Errorf("%w: thread %q", ErrNotFound, threadID)
	}
	now := time.Now().UTC()
	if err := b.store.RenameThread(ctx, threadID, title, agent, now); err != nil {
		return Thread{}, err
	}
	b.emit(Event{Kind: "thread", Type: "renamed", ThreadID: threadID, From: agent, Text: title, At: now})
	return b.store.GetThread(ctx, threadID)
}

// withTitles fills in each message's thread title.
func (b *Bus) withTitles(ctx context.Context, msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ThreadID)
	}
	titles, err := b.store.ThreadTitles(ctx, ids)
	if err != nil {
		b.log.Error("thread titles", "err", err)
		return msgs
	}
	for i := range msgs {
		msgs[i].ThreadTitle = titles[msgs[i].ThreadID]
	}
	return msgs
}
