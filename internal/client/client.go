// Package client is a Go client for the birdie HTTP API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"birdie/internal/bus"
)

// Lease is how long a session keeps messages it claimed before confirming them.
const Lease = 2 * time.Minute

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client

	mu      sync.RWMutex
	session string
}

// SetSession makes the client act for one session: its inbox is scoped to that
// session, what it sends can be answered there, and messages it claims must be
// confirmed with Ack.
func (c *Client) SetSession(id string) {
	c.mu.Lock()
	c.session = id
	c.mu.Unlock()
}

// Session returns the session the client acts for.
func (c *Client) Session() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.session
}

func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{}}
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status) }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("birdie daemon not reachable at %s (is `birdie serve` running?): %w", c.BaseURL, err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Me(ctx context.Context) (bus.Agent, error) {
	var a bus.Agent
	return a, c.do(ctx, http.MethodGet, "/v1/me", nil, &a)
}

func (c *Client) Agents(ctx context.Context) ([]bus.Agent, error) {
	var out struct{ Agents []bus.Agent }
	return out.Agents, c.do(ctx, http.MethodGet, "/v1/agents", nil, &out)
}

func (c *Client) Send(ctx context.Context, in bus.SendInput) (bus.Message, error) {
	if in.FromSession == "" {
		in.FromSession = c.Session()
	}
	var m bus.Message
	return m, c.do(ctx, http.MethodPost, "/v1/messages", in, &m)
}

// Inbox claims queued messages, long-polling up to wait (the server caps it).
func (c *Client) Inbox(ctx context.Context, threadID string, wait time.Duration) ([]bus.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, wait+30*time.Second)
	defer cancel()
	q := url.Values{"wait": {strconv.FormatFloat(wait.Seconds(), 'f', -1, 64)}}
	if threadID != "" {
		q.Set("thread_id", threadID)
	}
	if id := c.Session(); id != "" {
		q.Set("session", id)
		q.Set("lease", strconv.FormatFloat(Lease.Seconds(), 'f', -1, 64))
	}
	var out struct{ Messages []bus.Message }
	return out.Messages, c.do(ctx, http.MethodGet, "/v1/inbox?"+q.Encode(), nil, &out)
}

func (c *Client) Thread(ctx context.Context, threadID string) (bus.Thread, []bus.Message, error) {
	var out struct {
		Thread   bus.Thread
		Messages []bus.Message
	}
	err := c.do(ctx, http.MethodGet, "/v1/threads/"+url.PathEscape(threadID), nil, &out)
	return out.Thread, out.Messages, err
}

// RenameThread gives a thread the agent is in a new title.
func (c *Client) RenameThread(ctx context.Context, threadID, title string) (bus.Thread, error) {
	var t bus.Thread
	return t, c.do(ctx, http.MethodPatch, "/v1/threads/"+url.PathEscape(threadID), map[string]string{"title": title}, &t)
}

// Ack confirms the agent got a claimed message.
func (c *Client) Ack(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/messages/"+url.PathEscape(id)+"/ack", nil, nil)
}

// Nack returns a claimed message to the queue.
func (c *Client) Nack(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/messages/"+url.PathEscape(id)+"/nack", nil, nil)
}

// Unread counts messages waiting for this client's session.
func (c *Client) Unread(ctx context.Context) (int, error) {
	var out struct{ Unread int }
	path := "/v1/unread"
	if id := c.Session(); id != "" {
		path += "?session=" + url.QueryEscape(id)
	}
	return out.Unread, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ReportSession tells the bus this session is running.
func (c *Client) ReportSession(ctx context.Context, id string, in bus.SessionInput) error {
	return c.do(ctx, http.MethodPut, "/v1/sessions/"+url.PathEscape(id), in, nil)
}

// EndSession tells the bus this session has stopped.
func (c *Client) EndSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(id), nil, nil)
}
