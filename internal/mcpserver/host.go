package mcpserver

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"birdie/internal/bus"
	"birdie/internal/client"
)

// SessionInfo describes the conversation a bridge serves right now.
type SessionInfo struct {
	ID, Name, Cwd string
	// State is one of the bus.State values, or empty when the host can't tell.
	State    string
	ActiveAt time.Time
}

// Host is the agent program a bridge runs under. It knows which conversation
// the bridge serves and how to put a message in front of the model.
type Host interface {
	Session() SessionInfo
	// Tier says how soon the session sees a message right now.
	Tier() string
	// Push shows the message to the model. It is only called at TierPush.
	Push(ctx context.Context, m bus.Message) error
	// Observe sees the _meta of each tool call, which is the only place some
	// agents name their conversation.
	Observe(meta json.RawMessage)
}

// HostOptions configure DetectHost.
type HostOptions struct {
	// NoPush keeps messages from interrupting the session; they wait for
	// check_inbox.
	NoPush bool
}

// DetectHost picks the Host for the MCP client that started this bridge. The
// client's own name decides first, because environment variables are inherited
// when one agent starts another from its shell.
func DetectHost(client string, opts HostOptions) Host {
	name := strings.ToLower(client)
	switch {
	case strings.Contains(name, "codex"):
		return &codexHost{noPush: opts.NoPush, fallback: procSession()}
	case strings.Contains(name, "grok"):
		return &grokHost{fallback: procSession()}
	case strings.Contains(name, "antigravity"):
		return &antigravityHost{fallback: procSession()}
	case strings.Contains(name, "claude"):
		return &claudeHost{noPush: opts.NoPush, fallback: procSession()}
	case os.Getenv("GROK_SESSION_ID") != "":
		return &grokHost{fallback: procSession()}
	case os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE_SESSION_ID") != "":
		return &claudeHost{noPush: opts.NoPush, fallback: procSession()}
	}
	return &plainHost{info: procSession()}
}

func procSession() SessionInfo {
	cwd, _ := os.Getwd()
	return SessionInfo{ID: fmt.Sprintf("proc-%d", os.Getpid()), Name: slug(filepath.Base(cwd)), Cwd: cwd}
}

var slugBad = regexp.MustCompile(`[^a-z0-9._-]+`)

// slug turns a title or folder name into something usable in an address.
func slug(s string) string {
	s = strings.Trim(slugBad.ReplaceAllString(strings.ToLower(s), "-"), "-._")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-._")
	}
	return s
}

// plainHost is any other MCP client: tools work, and messages wait for check_inbox.
type plainHost struct{ info SessionInfo }

func (h *plainHost) Session() SessionInfo    { return h.info }
func (h *plainHost) Tier() string            { return bus.TierPull }
func (h *plainHost) Observe(json.RawMessage) {}
func (h *plainHost) Push(context.Context, bus.Message) error {
	return errors.New("this agent can't be pushed to")
}

// claudeHost is Claude Code. It keeps a registry entry for each running
// process, ~/.claude/sessions/<pid>.json, naming its conversation and inbox
// socket. The entry follows the conversation through /clear.
type claudeHost struct {
	noPush   bool
	fallback SessionInfo

	once sync.Once
	pid  int
}

type claudeEntry struct {
	SessionID       string `json:"sessionId"`
	Name            string `json:"name"`
	NameSource      string `json:"nameSource"`
	Cwd             string `json:"cwd"`
	Status          string `json:"status"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
	Socket          string `json:"messagingSocketPath"`
}

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func (h *claudeHost) entry() (claudeEntry, bool) {
	h.once.Do(func() { h.pid = claudeAncestor() })
	var e claudeEntry
	if h.pid == 0 {
		return e, false
	}
	b, err := os.ReadFile(filepath.Join(claudeDir(), "sessions", fmt.Sprintf("%d.json", h.pid)))
	if err != nil || json.Unmarshal(b, &e) != nil {
		return claudeEntry{}, false
	}
	return e, true
}

func (h *claudeHost) Session() SessionInfo {
	info := h.fallback
	e, ok := h.entry()
	if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		info.ID = id
	}
	if !ok {
		return info
	}
	if e.SessionID != "" {
		info.ID = e.SessionID
	}
	if e.Cwd != "" {
		info.Cwd = e.Cwd
		info.Name = slug(filepath.Base(e.Cwd))
	}
	// A name someone chose wins. One Claude made up from the conversation is
	// long, so it only stands in when the folder gives nothing.
	if e.Name != "" && (e.NameSource != "auto" && e.NameSource != "derived" || info.Name == "") {
		info.Name = slug(e.Name)
	}
	info.State = claudeStates[e.Status]
	switch {
	case e.Status == "busy":
		info.ActiveAt = time.Now()
	case e.StatusUpdatedAt > 0:
		info.ActiveAt = time.UnixMilli(e.StatusUpdatedAt)
	}
	return info
}

// claudeStates maps the status in Claude Code's registry entry to the bus's.
var claudeStates = map[string]string{"busy": bus.StateBusy, "idle": bus.StateIdle, "waiting": bus.StateWaiting}

func (h *claudeHost) socket() string {
	if s := os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"); s != "" {
		return s
	}
	e, _ := h.entry()
	return e.Socket
}

func (h *claudeHost) Tier() string {
	if h.noPush || runtime.GOOS == "windows" || h.socket() == "" {
		return bus.TierPull
	}
	return bus.TierPush
}

// Push writes a user frame to the session's inbox socket: Claude Code shows it
// between tool calls, or starts a turn if the session is idle, and marks it as
// coming from a peer. No token is sent on purpose, so a session that skips
// permission prompts still holds the message for its user to review.
func (h *claudeHost) Push(ctx context.Context, m bus.Message) error {
	path := h.socket()
	if path == "" {
		return errors.New("no inbox socket")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	frame, err := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]string{"role": "user", "content": MessageText(m)},
	})
	if err != nil {
		return err
	}
	_, err = conn.Write(append(frame, '\n'))
	return err
}

func (h *claudeHost) Observe(json.RawMessage) {}

func claudeAncestor() int {
	pid := os.Getppid()
	for depth := 0; depth < 8 && pid > 1; depth++ {
		ppid, argv, err := procInfo(pid)
		if err != nil {
			return 0
		}
		if isClaudeCode(argv) {
			return pid
		}
		pid = ppid
	}
	return 0
}

// maxArgBytes keeps a message inside what one command-line argument can hold.
const maxArgBytes = 100 << 10

// codexHost is Codex. Its bridge isn't told the conversation until a tool call
// names it, so a session becomes reachable by push after its first tool call.
// `codex queue` then adds to the thread and starts a turn if it is idle.
type codexHost struct {
	noPush   bool
	fallback SessionInfo

	mu     sync.Mutex
	thread string
}

func (h *codexHost) current() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.thread != "" {
		return h.thread
	}
	return os.Getenv("CODEX_THREAD_ID")
}

func (h *codexHost) Session() SessionInfo {
	info := h.fallback
	if t := h.current(); t != "" {
		info.ID = t
	}
	return info
}

func (h *codexHost) Tier() string {
	if h.noPush || h.current() == "" {
		return bus.TierPull
	}
	if _, err := exec.LookPath("codex"); err != nil {
		return bus.TierPull
	}
	return bus.TierPush
}

func (h *codexHost) Push(ctx context.Context, m bus.Message) error {
	text := MessageText(m)
	if len(text) > maxArgBytes {
		text = fmt.Sprintf("<birdie-message from=%q message_id=%q thread_id=%q>\nThis message is too long to show here. Read it with the birdie get_thread tool, and answer with send_message, in_reply_to %q.\n</birdie-message>", m.From, m.ID, m.ThreadID, m.ID)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "queue", "--thread", h.current(), "--message", text)
	cmd.Dir = h.fallback.Cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("codex queue: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (h *codexHost) Observe(meta json.RawMessage) {
	var m struct {
		Turn struct {
			SessionID string `json:"session_id"`
			ThreadID  string `json:"thread_id"`
		} `json:"x-codex-turn-metadata"`
		SessionID string `json:"sessionId"`
		ThreadID  string `json:"threadId"`
	}
	if json.Unmarshal(meta, &m) != nil {
		return
	}
	// The session id is the root conversation; a subagent's thread id differs.
	for _, id := range []string{m.Turn.SessionID, m.SessionID, m.Turn.ThreadID, m.ThreadID} {
		if id != "" {
			h.mu.Lock()
			h.thread = id
			h.mu.Unlock()
			return
		}
	}
}

// GrokHooksPath is where `birdie setup grok` puts its hooks.
func GrokHooksPath() string {
	dir := os.Getenv("GROK_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".grok")
	}
	return filepath.Join(dir, "hooks", "birdie.json")
}

// grokHost is the Grok CLI. With the hooks from `birdie setup grok`, it sees
// messages at its next tool call or end of turn. Nothing wakes an idle session.
type grokHost struct{ fallback SessionInfo }

func (h *grokHost) Session() SessionInfo {
	info := h.fallback
	if id := os.Getenv("GROK_SESSION_ID"); id != "" {
		info.ID = id
	}
	return info
}

func (h *grokHost) Tier() string {
	if _, err := os.Stat(GrokHooksPath()); err == nil {
		return bus.TierTurn
	}
	return bus.TierPull
}

func (h *grokHost) Observe(json.RawMessage) {}
func (h *grokHost) Push(context.Context, bus.Message) error {
	return errors.New("grok sessions get messages through hooks")
}

// AntigravityDir is Antigravity's global customization folder under home.
// The CLI, the IDE and the app all read its mcp_config.json, hooks.json and
// skills.
func AntigravityDir(home string) string { return filepath.Join(home, ".gemini", "config") }

// AntigravityHooksPath is the hooks file `birdie setup antigravity` adds to.
func AntigravityHooksPath(home string) string {
	return filepath.Join(AntigravityDir(home), "hooks.json")
}

// AntigravityHooked says whether the hooks file has a birdie entry that is
// switched on and runs something. The file is shared with other hooks, so
// being there isn't enough.
func AntigravityHooked(home string) bool {
	b, err := os.ReadFile(AntigravityHooksPath(home))
	if err != nil {
		return false
	}
	var hooks map[string]*struct {
		Enabled       *bool             `json:"enabled"`
		PreInvocation []json.RawMessage `json:"PreInvocation"`
		Stop          []json.RawMessage `json:"Stop"`
	}
	if json.Unmarshal(b, &hooks) != nil {
		return false
	}
	h := hooks["birdie"]
	if h == nil || (h.Enabled != nil && !*h.Enabled) {
		return false
	}
	return len(h.PreInvocation) > 0 || len(h.Stop) > 0
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// antigravityConversations is where birdie's hooks note the conversation a
// running agy process started with, one file per process. Antigravity tells
// hooks the conversation, but tells MCP servers only once a tool is called.
func antigravityConversations() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "birdie", "antigravity")
}

// agyEntry is what a hook notes for one agy process. The process's start
// time tells it apart from a later process given the same pid.
type agyEntry struct{ conv, start string }

func readAgyEntry(pid int) (agyEntry, bool) {
	b, err := os.ReadFile(filepath.Join(antigravityConversations(), fmt.Sprint(pid)))
	if err != nil {
		return agyEntry{}, false
	}
	conv, start, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if conv == "" {
		return agyEntry{}, false
	}
	return agyEntry{conv, start}, true
}

// NoteAntigravityConversation is called from a hook. The first conversation a
// hook runs in, under an agy process, is the one the user started: subagents
// come later, and their hooks run with their own conversation id but nothing
// saying they're subagents. So only that first one is noted, for the
// process's bridge to act for, and reported to the bus at once so its first
// turn can already take messages. A later conversation, a subagent's or one
// the user started with /new, is left to the bridge, which learns the root
// conversation from the next tool call.
func NoteAntigravityConversation(ctx context.Context, c *client.Client, id, cwd string) error {
	pid := findAgy()
	if pid == 0 || id == "" {
		return nil // not under the agy CLI, or on a system where it can't be told
	}
	start := procStart(pid)
	if e, ok := readAgyEntry(pid); ok && e.start == start {
		return nil
	}
	dir := antigravityConversations()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sweepAgyEntries(dir)
	tmp, err := os.CreateTemp(dir, fmt.Sprintf(".%d-*", pid))
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(id + "\n" + start)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	path := filepath.Join(dir, fmt.Sprint(pid))
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	err = c.ReportSession(ctx, id, bus.SessionInput{Name: slug(filepath.Base(cwd)), Cwd: cwd, Branch: gitBranch(cwd), Tier: bus.TierTurn})
	if err != nil {
		os.Remove(path) // so the next hook tries again
	}
	return err
}

// sweepAgyEntries removes the files of agy processes that have ended.
func sweepAgyEntries(dir string) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, f := range files {
		pid, err := strconv.Atoi(f.Name())
		if err != nil {
			continue
		}
		if e, ok := readAgyEntry(pid); !ok || e.start != procStart(pid) {
			os.Remove(filepath.Join(dir, f.Name()))
		}
	}
}

// findAgy finds the agy process this one runs under; a variable for tests.
var findAgy = agyAncestor

func agyAncestor() int {
	pid := os.Getppid()
	for depth := 0; depth < 8 && pid > 1; depth++ {
		ppid, argv, err := procInfo(pid)
		if err != nil {
			return 0
		}
		if len(argv) > 0 && strings.TrimSuffix(filepath.Base(argv[0]), ".exe") == "agy" {
			return pid
		}
		pid = ppid
	}
	return 0
}

// antigravityHost is the Antigravity CLI. A tool call's _meta names the root
// conversation; before the first one, the bridge acts for the conversation a
// hook noted for its agy process. With the hooks from `birdie setup
// antigravity`, the session sees messages before its next model call or when
// it finishes a turn. Nothing wakes an idle session.
type antigravityHost struct {
	fallback SessionInfo

	once  sync.Once
	pid   int
	start string

	mu   sync.Mutex
	conv string
}

func (h *antigravityHost) current() string {
	h.mu.Lock()
	conv := h.conv
	h.mu.Unlock()
	if conv != "" {
		return conv
	}
	h.once.Do(func() {
		if h.pid = findAgy(); h.pid != 0 {
			h.start = procStart(h.pid)
		}
	})
	if h.pid != 0 {
		if e, ok := readAgyEntry(h.pid); ok && e.start == h.start {
			return e.conv
		}
	}
	return ""
}

func (h *antigravityHost) Session() SessionInfo {
	info := h.fallback
	if c := h.current(); c != "" {
		info.ID = c
	}
	return info
}

func (h *antigravityHost) Tier() string {
	if AntigravityHooked(homeDir()) {
		return bus.TierTurn
	}
	return bus.TierPull
}

func (h *antigravityHost) Observe(meta json.RawMessage) {
	var m struct {
		Root string `json:"antigravity.google/root_conversation_id"`
		Conv string `json:"antigravity.google/conversation_id"`
	}
	if json.Unmarshal(meta, &m) != nil {
		return
	}
	// A subagent has its own conversation; its root is the one the user sees.
	if id := cmp.Or(m.Root, m.Conv); id != "" {
		h.mu.Lock()
		h.conv = id
		h.mu.Unlock()
	}
}

func (h *antigravityHost) Push(context.Context, bus.Message) error {
	return errors.New("antigravity sessions get messages through hooks")
}
