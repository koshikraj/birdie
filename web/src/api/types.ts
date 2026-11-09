// The web app's API, served by `birdie serve` under /ui/api. These types
// mirror the JSON the Go handlers in internal/webui write. Times are RFC 3339
// strings; a time that hasn't happened is null.

export type Delivery = 'pull' | 'webhook' | 'acp' | string;

/** What a session is doing, when its agent says. Only Claude Code does. */
export type SessionState = 'busy' | 'idle' | 'waiting' | '';

export interface Session {
  id: string;
  agent: string;
  /** How other agents address it: agent/name. */
  name?: string;
  cwd?: string;
  branch?: string;
  tier: 'push' | 'turn' | 'pull';
  state?: SessionState;
  /** The session that takes new conversations. */
  default?: boolean;
  /** When its user or model last did something; for idle or waiting, when it stopped. */
  active_at: string;
  last_seen: string;
  /** Set when it closed. A session is running when this is unset and last_seen is within live_ttl_seconds. */
  ended_at?: string;
  /** Computed by the server: running right now. */
  live: boolean;
}

/** How delivery to one agent is going. */
export interface Health {
  /** Messages it hasn't got yet, and when the oldest was sent. */
  waiting: number;
  oldest: string | null;
  /** When a message last reached it. */
  delivered: string | null;
  /** Why the latest try failed, if nothing got through since; error_id is that message. */
  error: string;
  error_id: string;
  error_at: string | null;
  /** When the next try is due; null once birdie gave up on that message. */
  retry: string | null;
  /** Messages given up on in the last day. */
  gave_up: number;
}

export interface Agent {
  name: string;
  description?: string;
  delivery: Delivery;
  created_at: string;
  /** Pull agents only: running sessions first (default marked), then ones that stopped in the last day. */
  sessions: Session[];
  health: Health;
}

export type MessageStatus = 'queued' | 'claimed' | 'delivered' | 'replied' | 'failed';

/** A message with how far it got. */
export interface Tracked {
  id: string;
  thread_id: string;
  in_reply_to?: string;
  from: string;
  to: string;
  from_session?: string;
  to_session?: string;
  /** "agent/name" for display, resolved by the server. */
  from_label: string;
  to_label: string;
  content: string;
  status: MessageStatus;
  created_at: string;
  delivered_at: string | null;
  /** The session that took it, as a label, or "" when none did. */
  taken_by: string;
  attempts: number;
  /** Why the latest try failed, and when. */
  error: string;
  error_at: string | null;
  /** When the next try is due, while queued after an error. */
  retry: string | null;
}

export interface Pair {
  a: string;
  b: string;
  /** Messages between the two today, both directions. */
  count: number;
}

export interface Overview {
  version: string;
  /** Where the bus listens, such as 127.0.0.1:7777. */
  address: string;
  /** The agent the web app sends as. */
  me: string;
  live_ttl_seconds: number;
  agents: Agent[];
  /** Messages that haven't reached their recipient, oldest first, at most 10. */
  waiting: { total: number; messages: Tracked[] };
  pairs: Pair[];
  today: { sent: number; last_at: string | null };
}

export interface ThreadSummary {
  id: string;
  participants: string[];
  count: number;
  last_at: string;
  last_from: string;
  last_text: string;
  /** The last message's status. */
  status: MessageStatus;
  /** True when a message in it is retrying or failed. */
  problem: boolean;
}

/** GET /ui/api/threads?limit=N */
export interface ThreadList { threads: ThreadSummary[] }

/** GET /ui/api/threads/{thread or message id} */
export interface Thread { id: string; messages: Tracked[] }

/** GET /ui/api/messages?limit=N: newest first. */
export interface RecentMessages { messages: Tracked[] }

/** POST /ui/api/messages */
export interface SendInput { to: string; content: string }
export interface SendResult {
  id: string;
  thread_id: string;
  to: string;
  to_session?: string;
  status: MessageStatus;
  /** What birdie prints after a send, such as "queued: claude/birdie gets this right away". */
  note: string;
}

/** GET /ui/api/tools: agent programs found on this machine. */
export interface Tool {
  /** The bus name `birdie setup` gives it. */
  name: string;
  product: string;
  found: boolean;
  connected: boolean;
  /** The command that connects it. */
  setup: string;
}
export interface ToolList { tools: Tool[] }

/** Errors come back as { error } with a 4xx or 5xx status. 401 means sign in again with `birdie ui`. */
export interface ApiError { error: string }

/**
 * GET /ui/api/events is a Server-Sent Events stream. Each event's `event:` is
 * "message" or "session", and its data is one of these.
 */
export interface MessageEvent {
  kind: 'message';
  /** queued: sent. claimed: taken, not confirmed. delivered. replied: answered. retrying: a try failed. failed: given up. */
  type: 'queued' | 'claimed' | 'delivered' | 'replied' | 'retrying' | 'failed';
  id: string;
  thread_id: string;
  in_reply_to?: string;
  from: string;
  to: string;
  from_label: string;
  to_label: string;
  /** The start of the message, at most 200 characters. */
  text: string;
  error?: string;
  at: string;
}

export interface SessionEvent {
  kind: 'session';
  type: 'reported' | 'ended';
  agent: string;
  id: string;
  name?: string;
  state?: SessionState;
  at: string;
}

export type BusEvent = MessageEvent | SessionEvent;
