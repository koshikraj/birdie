// Package store implements bus.Store on SQLite (pure Go, no cgo), so the
// binary cross-compiles to every platform Go supports.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"birdie/internal/bus"
)

const schema = `
CREATE TABLE IF NOT EXISTS agents (
	name       TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	delivery   TEXT NOT NULL DEFAULT 'pull',
	config     TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
	seq             INTEGER PRIMARY KEY AUTOINCREMENT,
	id              TEXT NOT NULL UNIQUE,
	thread_id       TEXT NOT NULL,
	in_reply_to     TEXT NOT NULL DEFAULT '',
	sender          TEXT NOT NULL,
	recipient       TEXT NOT NULL,
	content         TEXT NOT NULL,
	meta            TEXT NOT NULL DEFAULT '{}',
	status          TEXT NOT NULL,
	attempts        INTEGER NOT NULL DEFAULT 0,
	last_error      TEXT NOT NULL DEFAULT '',
	next_attempt_at INTEGER NOT NULL DEFAULT 0,
	created_at      INTEGER NOT NULL,
	delivered_at    INTEGER
);
CREATE INDEX IF NOT EXISTS messages_inbox ON messages(recipient, status, seq);
CREATE INDEX IF NOT EXISTS messages_thread ON messages(thread_id, seq);
CREATE TABLE IF NOT EXISTS state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	agent     TEXT NOT NULL,
	id        TEXT NOT NULL,
	name      TEXT NOT NULL DEFAULT '',
	cwd       TEXT NOT NULL DEFAULT '',
	tier      TEXT NOT NULL DEFAULT 'pull',
	active_at INTEGER NOT NULL,
	last_seen INTEGER NOT NULL,
	PRIMARY KEY (agent, id)
);
`

// columns added to existing databases, oldest first.
var addedColumns = []struct{ table, column, def string }{
	{"messages", "from_session", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "to_session", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "lease_until", "INTEGER NOT NULL DEFAULT 0"},
	{"sessions", "branch", "TEXT NOT NULL DEFAULT ''"},
	{"sessions", "state", "TEXT NOT NULL DEFAULT ''"},
}

// sessionRetention is how long a session that stopped reporting is remembered.
const sessionRetention = 24 * time.Hour

const (
	msgCols     = `m.id, m.thread_id, m.in_reply_to, m.sender, m.recipient, m.from_session, m.to_session, m.content, m.meta, m.status, m.attempts, m.created_at`
	agentCols   = `a.name, a.token_hash, a.delivery, a.config, a.created_at`
	sessionCols = `agent, id, name, cwd, branch, tier, state, active_at, last_seen`
)

type SQLite struct{ db *sql.DB }

var _ bus.Store = (*SQLite)(nil)

func Open(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// One connection serializes writers in-process; WAL plus busy_timeout
	// handles the CLI touching the same file concurrently.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	if err := addColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return &SQLite{db: db}, nil
}

func addColumns(db *sql.DB) error {
	for _, c := range addedColumns {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.column).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` ` + c.def); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLite) Close() error { return s.db.Close() }

type scanner interface{ Scan(...any) error }

func scanAgent(r scanner) (bus.Agent, error) {
	var a bus.Agent
	var cfg string
	var created int64
	if err := r.Scan(&a.Name, &a.TokenHash, &a.Delivery, &cfg, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return a, bus.ErrNotFound
		}
		return a, err
	}
	a.CreatedAt = time.UnixMilli(created).UTC()
	if err := json.Unmarshal([]byte(cfg), &a.Config); err != nil {
		return a, err
	}
	a.Description = a.Config["description"]
	return a, nil
}

func scanMessage(r scanner) (bus.Message, error) {
	var m bus.Message
	var meta string
	var created int64
	if err := r.Scan(&m.ID, &m.ThreadID, &m.InReplyTo, &m.From, &m.To, &m.FromSession, &m.ToSession, &m.Content, &meta, &m.Status, &m.Attempts, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return m, bus.ErrNotFound
		}
		return m, err
	}
	m.CreatedAt = time.UnixMilli(created).UTC()
	if err := json.Unmarshal([]byte(meta), &m.Meta); err != nil {
		return m, err
	}
	if len(m.Meta) == 0 {
		m.Meta = nil
	}
	return m, nil
}

func collectMessages(rows *sql.Rows, err error) ([]bus.Message, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := []bus.Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *SQLite) CreateAgent(ctx context.Context, a bus.Agent) error {
	cfg, err := json.Marshal(a.Config)
	if err != nil {
		return err
	}
	if a.Config == nil {
		cfg = []byte("{}")
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO agents (name, token_hash, delivery, config, created_at) VALUES (?, ?, ?, ?, ?)`,
		a.Name, a.TokenHash, a.Delivery, string(cfg), a.CreatedAt.UnixMilli())
	return err
}

func (s *SQLite) DeleteAgent(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return bus.ErrNotFound
	}
	return nil
}

func (s *SQLite) GetAgent(ctx context.Context, name string) (bus.Agent, error) {
	return scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents a WHERE a.name = ?`, name))
}

func (s *SQLite) AgentByTokenHash(ctx context.Context, hash string) (bus.Agent, error) {
	return scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents a WHERE a.token_hash = ?`, hash))
}

func (s *SQLite) ListAgents(ctx context.Context) ([]bus.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentCols+` FROM agents a ORDER BY a.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := []bus.Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *SQLite) InsertMessage(ctx context.Context, m bus.Message) error {
	meta := []byte("{}")
	if len(m.Meta) > 0 {
		var err error
		if meta, err = json.Marshal(m.Meta); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, thread_id, in_reply_to, sender, recipient, from_session, to_session, content, meta, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ThreadID, m.InReplyTo, m.From, m.To, m.FromSession, m.ToSession, m.Content, string(meta), m.Status, m.CreatedAt.UnixMilli())
	return err
}

func (s *SQLite) GetMessage(ctx context.Context, id string) (bus.Message, error) {
	return scanMessage(s.db.QueryRowContext(ctx, `SELECT `+msgCols+` FROM messages m WHERE m.id = ?`, id))
}

func (s *SQLite) ThreadAccess(ctx context.Context, threadID, agent string) (bool, bool, error) {
	var total, mine int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(sender = ? OR recipient = ?), 0) FROM messages WHERE thread_id = ?`,
		agent, agent, threadID).Scan(&total, &mine)
	return total > 0, mine > 0, err
}

func (s *SQLite) Thread(ctx context.Context, threadID, agent string) ([]bus.Message, error) {
	return collectMessages(s.db.QueryContext(ctx,
		`SELECT `+msgCols+` FROM messages m WHERE m.thread_id = ? AND (m.sender = ? OR m.recipient = ?) ORDER BY m.seq`,
		threadID, agent, agent))
}

// inboxWhere is the condition for messages a ClaimFilter may take. It uses the
// messages alias m.
func inboxWhere(f bus.ClaimFilter, now time.Time) (string, []any) {
	q := ` m.recipient = ? AND (m.status = 'queued' OR (m.status = 'claimed' AND m.lease_until > 0 AND m.lease_until <= ?))`
	args := []any{f.Agent, now.UnixMilli()}
	if f.Thread != "" {
		q += ` AND m.thread_id = ?`
		args = append(args, f.Thread)
	}
	if f.Session != "" {
		if f.Unpinned {
			// Mail for a session that has gone falls to this one too.
			q += ` AND (m.to_session = ? OR m.to_session = '' OR m.to_session NOT IN
				(SELECT id FROM sessions WHERE agent = ? AND last_seen >= ?))`
			args = append(args, f.Session, f.Agent, f.LiveSince.UnixMilli())
		} else {
			q += ` AND m.to_session = ?`
			args = append(args, f.Session)
		}
	}
	return q, args
}

func (s *SQLite) ClaimInbox(ctx context.Context, f bus.ClaimFilter, now time.Time) ([]bus.Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	where, args := inboxWhere(f, now)
	msgs, err := collectMessages(tx.QueryContext(ctx,
		`SELECT `+msgCols+` FROM messages m WHERE`+where+` ORDER BY m.seq LIMIT ?`, append(args, f.Limit)...))
	if err != nil || len(msgs) == 0 {
		return msgs, err
	}
	for i := range msgs {
		if f.LeaseUntil.IsZero() {
			_, err = tx.ExecContext(ctx, `UPDATE messages SET status = 'delivered', delivered_at = ? WHERE id = ?`, now.UnixMilli(), msgs[i].ID)
			msgs[i].Status = bus.StatusDelivered
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE messages SET status = 'claimed', lease_until = ? WHERE id = ?`, f.LeaseUntil.UnixMilli(), msgs[i].ID)
			msgs[i].Status = bus.StatusClaimed
		}
		if err != nil {
			return nil, err
		}
	}
	return msgs, tx.Commit()
}

func (s *SQLite) CountInbox(ctx context.Context, f bus.ClaimFilter, now time.Time) (int, error) {
	where, args := inboxWhere(f, now)
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m WHERE`+where, args...).Scan(&n)
	return n, err
}

func (s *SQLite) CountSent(ctx context.Context, from, to string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE sender = ? AND recipient = ? AND created_at >= ?`, from, to, since.UnixMilli()).Scan(&n)
	return n, err
}

func (s *SQLite) DueOutbound(ctx context.Context, now time.Time, limit int) ([]bus.Outbound, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+msgCols+`, `+agentCols+` FROM messages m JOIN agents a ON a.name = m.recipient
		 WHERE m.status = 'queued' AND a.delivery != 'pull' AND m.next_attempt_at <= ?
		 ORDER BY m.seq LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []bus.Outbound
	for rows.Next() {
		var o bus.Outbound
		var meta, cfg string
		var mCreated, aCreated int64
		m, a := &o.Message, &o.Agent
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.InReplyTo, &m.From, &m.To, &m.FromSession, &m.ToSession, &m.Content, &meta, &m.Status, &m.Attempts, &mCreated,
			&a.Name, &a.TokenHash, &a.Delivery, &cfg, &aCreated); err != nil {
			return nil, err
		}
		m.CreatedAt, a.CreatedAt = time.UnixMilli(mCreated).UTC(), time.UnixMilli(aCreated).UTC()
		if err := json.Unmarshal([]byte(meta), &m.Meta); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(cfg), &a.Config); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *SQLite) MarkDelivered(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET status = 'delivered', delivered_at = ?, last_error = '' WHERE id = ?`, at.UnixMilli(), id)
	return err
}

func (s *SQLite) MarkClaimed(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET status = 'claimed', lease_until = 0, last_error = '' WHERE id = ?`, id)
	return err
}

func (s *SQLite) Settle(ctx context.Context, id, agent string, ok bool, at time.Time) (bool, error) {
	var res sql.Result
	var err error
	if ok {
		res, err = s.db.ExecContext(ctx, `UPDATE messages SET status = 'delivered', delivered_at = ?, lease_until = 0
			WHERE id = ? AND recipient = ? AND status = 'claimed'`, at.UnixMilli(), id, agent)
	} else {
		res, err = s.db.ExecContext(ctx, `UPDATE messages SET status = 'queued', lease_until = 0
			WHERE id = ? AND recipient = ? AND status = 'claimed'`, id, agent)
	}
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *SQLite) MarkReplied(ctx context.Context, id, agent string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET status = 'replied', lease_until = 0,
		delivered_at = COALESCE(delivered_at, ?) WHERE id = ? AND recipient = ? AND status != 'failed'`, time.Now().UnixMilli(), id, agent)
	return err
}

func (s *SQLite) ReleaseOutbound(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET status = 'queued', next_attempt_at = 0 WHERE status = 'claimed'
		AND recipient IN (SELECT name FROM agents WHERE delivery != 'pull')`)
	return err
}

func (s *SQLite) UpsertSession(ctx context.Context, ses bus.Session) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE last_seen < ?`, ses.LastSeen.Add(-sessionRetention).UnixMilli()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (agent, id, name, cwd, branch, tier, state, active_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent, id) DO UPDATE SET name = excluded.name, cwd = excluded.cwd, branch = excluded.branch, tier = excluded.tier,
			state = excluded.state, active_at = MAX(active_at, excluded.active_at), last_seen = excluded.last_seen`,
		ses.Agent, ses.ID, ses.Name, ses.Cwd, ses.Branch, ses.Tier, ses.State, ses.ActiveAt.UnixMilli(), ses.LastSeen.UnixMilli())
	return err
}

func (s *SQLite) DeleteSession(ctx context.Context, agent, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE agent = ? AND id = ?`, agent, id)
	return err
}

func (s *SQLite) LiveSessions(ctx context.Context, agent string, since time.Time) ([]bus.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions
		WHERE agent = ? AND last_seen >= ? ORDER BY active_at DESC, id`, agent, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []bus.Session
	for rows.Next() {
		var ses bus.Session
		var active, seen int64
		if err := rows.Scan(&ses.Agent, &ses.ID, &ses.Name, &ses.Cwd, &ses.Branch, &ses.Tier, &ses.State, &active, &seen); err != nil {
			return nil, err
		}
		ses.ActiveAt, ses.LastSeen = time.UnixMilli(active).UTC(), time.UnixMilli(seen).UTC()
		out = append(out, ses)
	}
	return out, rows.Err()
}

func (s *SQLite) RecordFailure(ctx context.Context, id, errMsg string, next time.Time, failed bool) error {
	status := bus.StatusQueued
	if failed {
		status = bus.StatusFailed
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET attempts = attempts + 1, last_error = ?, next_attempt_at = ?, status = ? WHERE id = ?`,
		errMsg, next.UnixMilli(), status, id)
	return err
}

// GetState returns adapter state saved under key, or bus.ErrNotFound.
func (s *SQLite) GetState(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", bus.ErrNotFound
	}
	return v, err
}

func (s *SQLite) SetState(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// SetAgentConfig sets one config key for an agent; an empty value removes it.
// SetAgentDelivery changes how an agent gets its messages. Moving a pull agent
// to a pushed delivery sends whatever is still queued for it.
func (s *SQLite) SetAgentDelivery(ctx context.Context, name, delivery string) error {
	if _, err := s.GetAgent(ctx, name); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET delivery = ? WHERE name = ?`, delivery, name)
	return err
}

func (s *SQLite) SetAgentConfig(ctx context.Context, name, key, value string) error {
	a, err := s.GetAgent(ctx, name)
	if err != nil {
		return err
	}
	if a.Config == nil {
		a.Config = map[string]string{}
	}
	if value == "" {
		delete(a.Config, key)
	} else {
		a.Config[key] = value
	}
	cfg, err := json.Marshal(a.Config)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE agents SET config = ? WHERE name = ?`, string(cfg), name)
	return err
}
