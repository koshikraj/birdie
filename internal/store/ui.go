package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"birdie/internal/bus"
)

// Queries behind birdie's web app. Like the status commands, they read every
// agent's messages: only the machine's owner gets to them.

// RecentMessages returns up to limit messages, newest first.
func (s *SQLite) RecentMessages(ctx context.Context, limit int) ([]Tracked, error) {
	return collectTracked(s.db.QueryContext(ctx,
		`SELECT `+msgCols+`, `+trackCols+` FROM messages m ORDER BY m.seq DESC LIMIT ?`, limit))
}

// ThreadSummary is one conversation, as a list shows it.
type ThreadSummary struct {
	ID           string
	Participants []string
	Count        int
	Last         Tracked
	// Problem is set when a message in it is retrying or failed.
	Problem bool
}

// RecentThreads returns up to limit threads, the most recently active first.
func (s *SQLite) RecentThreads(ctx context.Context, limit int) ([]ThreadSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.thread_id, t.n, t.problem, t.senders, t.recipients, `+msgCols+`, `+trackCols+`
		FROM (SELECT thread_id, COUNT(*) AS n, MAX(seq) AS last,
				MAX(status = 'failed' OR (status = 'queued' AND last_error != '')) AS problem,
				GROUP_CONCAT(DISTINCT sender) AS senders, GROUP_CONCAT(DISTINCT recipient) AS recipients
			FROM messages GROUP BY thread_id ORDER BY last DESC LIMIT ?) t
		JOIN messages m ON m.seq = t.last
		ORDER BY t.last DESC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThreadSummary{}
	for rows.Next() {
		var ts ThreadSummary
		var problem int
		var senders, recipients string
		var meta string
		var created, delivered, errorAt, next int64
		m := &ts.Last.Message
		if err := rows.Scan(&ts.ID, &ts.Count, &problem, &senders, &recipients,
			&m.ID, &m.ThreadID, &m.InReplyTo, &m.From, &m.To, &m.FromSession, &m.ToSession, &m.Content, &meta, &m.Status, &m.Attempts, &created,
			&delivered, &ts.Last.TakenBy, &ts.Last.Error, &errorAt, &next); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(created).UTC()
		ts.Last.Delivered, ts.Last.ErrorAt = unixMilli(delivered), unixMilli(errorAt)
		if m.Status == bus.StatusQueued && ts.Last.Error != "" {
			ts.Last.Retry = unixMilli(next)
		}
		ts.Problem = problem != 0
		seen := map[string]bool{}
		for _, name := range strings.Split(senders+","+recipients, ",") {
			if name != "" && !seen[name] {
				seen[name] = true
				ts.Participants = append(ts.Participants, name)
			}
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// Pair counts the messages between two agents, both ways; A sorts before B.
type Pair struct {
	A, B  string
	Count int
}

// PairsSince counts messages between each two agents since a time. Notices
// from birdie itself don't count.
func (s *SQLite) PairsSince(ctx context.Context, since time.Time) ([]Pair, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT MIN(sender, recipient), MAX(sender, recipient), COUNT(*) FROM messages
		WHERE created_at >= ? AND sender NOT IN (?, ?) AND recipient NOT IN (?, ?) AND sender != recipient
		GROUP BY 1, 2 ORDER BY 3 DESC`,
		since.UnixMilli(), bus.SystemSender, bus.LegacySystemSender, bus.SystemSender, bus.LegacySystemSender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pair{}
	for rows.Next() {
		var p Pair
		if err := rows.Scan(&p.A, &p.B, &p.Count); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SentSince counts messages agents sent since a time, and when the latest was.
func (s *SQLite) SentSince(ctx context.Context, since time.Time) (int, time.Time, error) {
	var n int
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(created_at), 0) FROM messages
		WHERE created_at >= ? AND sender NOT IN (?, ?)`, since.UnixMilli(), bus.SystemSender, bus.LegacySystemSender).Scan(&n, &last)
	return n, unixMilli(last), err
}

// TakeState returns the state saved under key and removes it, so it can be
// used once. It returns bus.ErrNotFound if there is none.
func (s *SQLite) TakeState(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `DELETE FROM state WHERE key = ? RETURNING value`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", bus.ErrNotFound
	}
	return v, err
}

// DeleteState removes the state saved under key, if any.
func (s *SQLite) DeleteState(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM state WHERE key = ?`, key)
	return err
}

// DeleteExpiredState removes the state saved under keys starting with prefix
// whose value, a Unix time, is before cutoff.
func (s *SQLite) DeleteExpiredState(ctx context.Context, prefix string, cutoff time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM state WHERE key LIKE ? ESCAPE '\' AND CAST(value AS INTEGER) < ?`,
		strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)+"%", cutoff.Unix())
	return err
}
