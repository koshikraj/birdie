import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type FormEvent, type KeyboardEvent, type MouseEvent, type RefObject } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiFailure, api } from '../api/client';
import { keys, useOverview, useThread, useThreads } from '../api/hooks';
import type { Agent, Delivery, ThreadSummary, Tracked } from '../api/types';
import { AgentTile } from '../components/AgentTile';
import { StatusChip } from '../components/bits';
import { Scroller } from '../components/Scroller';
import { Select } from '../components/Select';
import { Button } from '../ds';
import { Loading, Skeleton, Typing } from '../components/Signal';
import { TilePair } from '../components/TilePair';
import { TileStack } from '../components/TileStack';
import { ME, agentOf, agentsOf, defaultSession, homePath, labelOf, liveSessions, who } from '../lib/agents';
import { isSystem, mentionIn, mentionQuery, takeFocus } from '../lib/compose';
import { href } from '../lib/router';
import { MAX_TRIES, chipFor, errorText, progress, threadChip, troubled } from '../lib/status';
import { ago, ms, useNow } from '../lib/time';

export function MessagesPage({ id }: { id?: string }) {
  const threads = useThreads();
  const list = useMemo(() => threads.data?.threads ?? [], [threads.data]);
  const selected = id || list[0]?.id;
  const thread = useThread(selected);
  const now = useNow();
  // The route may name a message; the thread it belongs to is the one to mark.
  const threadId = thread.data?.id || selected;
  const row = useRef<HTMLDivElement>(null);
  const height = useFitHeight(row);

  return (
    <>
      <div className="page-head">
        <h1 className="page-title">Messages</h1>
        <p className="page-lede">Every conversation between your agents, and how far each message got.</p>
      </div>
      <div ref={row} className="row-wrap messages-row" style={height ? ({ '--fit-h': height + 'px' } as CSSProperties) : undefined}>
        <section aria-labelledby="threads-title" className="panel threads">
          <h2 id="threads-title" className="panel-title">Threads</h2>
          {!list.length ? (
            threads.isPending && !threads.isError ? (
              <div>
                <Loading label="Loading threads" />
                <Skeleton rows={4} />
              </div>
            ) : (
              <p className="panel-empty">{threads.isError ? "Threads can't load until birdie is reachable." : 'No messages yet. Send one from Agents, and its thread shows up here.'}</p>
            )
          ) : (
            <Scroller label="Threads" className="threads-scroll">
              <ul className="thread-items">
                {list.map((t) => (
                  <li key={t.id}>
                    <ThreadItem t={t} on={t.id === threadId} now={now} />
                  </li>
                ))}
              </ul>
            </Scroller>
          )}
        </section>
        <ThreadDetail key={selected} id={selected} target={id} query={thread} now={now} />
      </div>
    </>
  );
}

/** The page's two panels fill the window below where they start, so each scrolls on its own. */
function useFitHeight(ref: RefObject<HTMLElement>): number | undefined {
  const [h, setH] = useState<number>();
  useLayoutEffect(() => {
    const fit = () => {
      const el = ref.current;
      if (!el) return;
      const top = el.getBoundingClientRect().top + window.scrollY;
      const next = Math.max(440, Math.floor(window.innerHeight - top - 40));
      setH((h) => (h === next ? h : next));
    };
    fit();
    window.addEventListener('resize', fit);
    const ro = new ResizeObserver(fit);
    ro.observe(document.body);
    return () => {
      window.removeEventListener('resize', fit);
      ro.disconnect();
    };
  }, [ref]);
  return h;
}

/** The agents in a thread, each once, in the order they joined. */
function agentsIn(names: string[]): string[] {
  const out: string[] = [];
  for (const n of names) {
    const a = agentOf(n);
    if (!out.includes(a)) out.push(a);
  }
  return out;
}

function ThreadItem({ t, on, now }: { t: ThreadSummary; on: boolean; now: number }) {
  const agents = agentsIn(t.participants.length ? t.participants : [t.last_from]);
  return (
    <a href={href.thread(t.id)} className="thread-item" aria-current={on ? 'true' : undefined}>
      <TilePair a={agents[0]} b={agents[1]} more={agents.length - 2} size={30} />
      <span className="thread-item-body">
        <span className="thread-item-top">
          <span className="thread-item-title">{t.title || agents.map(labelOf).join(' ⇄ ')}</span>
          <span className="thread-item-when">{ago(t.last_at, now)}</span>
        </span>
        {t.title && <span className="thread-item-between">{agents.map(labelOf).join(' ⇄ ')}</span>}
        <span className="thread-item-preview">
          <span className="thread-item-who">{labelOf(agentOf(t.last_from))}:</span> {t.last_text}
        </span>
        <StatusChip chip={threadChip(t)} />
      </span>
    </a>
  );
}

function ThreadDetail({ id, target, query, now }: { id?: string; target?: string; query: ReturnType<typeof useThread>; now: number }) {
  const overview = useOverview();
  const me = overview.data?.me || ME;
  const kinds = useMemo(() => {
    const m = new Map<string, Delivery>();
    for (const a of overview.data?.agents ?? []) m.set(a.name, a.delivery);
    return m;
  }, [overview.data]);

  const data = query.data;
  const messages = useMemo(() => data?.messages ?? [], [data]);
  const agents = agentsIn(messages.flatMap((m) => [m.from, m.to]));
  // You're on the right when you're in the thread; otherwise whoever started it is.
  const right = agents.includes(me) ? me : agents[0];

  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const count = useRef(0);
  const [info, setInfo] = useState(false);
  // The message the reply box answers, picked with a message's Reply.
  const [replyTo, setReplyTo] = useState<Tracked | null>(null);
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set(target ? [target] : []));
  const toggle = (mid: string) =>
    setOpen((s) => {
      const next = new Set(s);
      if (next.has(mid)) next.delete(mid);
      else next.add(mid);
      return next;
    });

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const look = () => (atBottom.current = el.scrollTop + el.clientHeight >= el.scrollHeight - 48);
    el.addEventListener('scroll', look, { passive: true });
    return () => el.removeEventListener('scroll', look);
  }, [data]);

  // Open on the message the route names, else on the newest; follow new ones while you're at the bottom.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el || !messages.length) return;
    const first = count.current === 0;
    const grew = messages.length > count.current;
    count.current = messages.length;
    if (first && target) {
      const t = el.querySelector<HTMLElement>('#msg-' + CSS.escape(target));
      if (t) {
        el.scrollTop = t.offsetTop - el.clientHeight / 2 + t.offsetHeight / 2;
        return;
      }
    }
    if (first) el.scrollTop = el.scrollHeight;
    else if (grew && atBottom.current) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
  }, [messages, target]);

  if (!id) {
    return (
      <section aria-labelledby="thread-title" className="panel thread chat">
        <h2 id="thread-title" className="panel-title">No thread picked</h2>
        <p className="panel-empty">Pick a thread to see its messages.</p>
      </section>
    );
  }
  if (query.isError && !data) {
    const gone = query.error instanceof ApiFailure && query.error.status === 404;
    return (
      <section aria-labelledby="thread-title" className="panel thread chat">
        <h2 id="thread-title" className="panel-title">{gone ? "That thread isn't here" : "Couldn't load the thread"}</h2>
        <p className="panel-empty">{gone ? 'There is no thread or message with the id ' + id + '.' : String(query.error.message)}</p>
      </section>
    );
  }
  if (!data) {
    return (
      <section aria-labelledby="thread-title" className="panel thread chat" aria-busy="true">
        <h2 id="thread-title" className="visually-hidden">Loading the thread</h2>
        <Loading label="Loading the thread" />
      </section>
    );
  }

  const problems = messages.filter(troubled);
  const last = messages[messages.length - 1];
  const answering = last && last.status === 'claimed' && kinds.get(agentOf(last.to)) === 'acp' ? agentOf(last.to) : '';
  return (
    <section aria-labelledby="thread-title" className="panel thread chat">
      <div className="chat-head">
        {agents.length > 0 && <TileStack names={agents} size={38} max={4} className="chat-head-stack" />}
        <div className="chat-head-text">
          <ThreadTitle
            key={data.id + (data.title ?? '')}
            id={data.id}
            title={data.title || agents.map(labelOf).join(' ⇄ ') || data.id}
            canRename={messages.some((m) => m.from === me || m.to === me)}
          />
          <span className="chat-sub">
            {data.title && agents.map(labelOf).join(' ⇄ ') + ' · '}
            {messages.length} {messages.length === 1 ? 'message' : 'messages'}
            {last && ' · last ' + ago(last.created_at, now)}
          </span>
        </div>
        <button type="button" className="chat-info-button" aria-expanded={info} aria-controls="chat-info" onClick={() => setInfo((v) => !v)}>
          Details
        </button>
      </div>
      {info && (
        <div id="chat-info" className="chat-info">
          <span>Thread {data.id}</span>
          {data.created_by && <span>Started by {who(data.created_by)}{data.created_at && ' ' + ago(data.created_at, now)}</span>}
          {data.renamed_by && <span>Renamed by {who(data.renamed_by)}{data.renamed_at && ' ' + ago(data.renamed_at, now)}</span>}
          <span>Same as: birdie thread {data.id}</span>
        </div>
      )}

      {problems.map((m) => (
        <ProblemNote key={m.id} m={m} />
      ))}

      <Scroller ref={scroller} label="Messages in this thread" className="chat-scroll">
        <ol className="chat-list">
          {messages.map((m, i) => {
            const prev = messages[i - 1];
            const day = dayOf(m.created_at, now);
            const newDay = !prev || dayOf(prev.created_at, now) !== day;
            const from = agentOf(m.from);
            const starts = newDay || !prev || agentOf(prev.from) !== from || (ms(m.created_at) ?? 0) - (ms(prev.created_at) ?? 0) > 5 * 60000;
            return (
              <Bubble
                key={m.id}
                m={m}
                day={newDay ? day : ''}
                out={from === right}
                starts={starts}
                target={target === m.id}
                open={open.has(m.id)}
                toggle={() => toggle(m.id)}
                kind={kinds.get(agentOf(m.to))}
                now={now}
                reply={
                  canReply(m, me)
                    ? () => {
                        setReplyTo(m);
                        document.getElementById('compose-text')?.focus();
                      }
                    : undefined
                }
              />
            );
          })}
          {answering && (
            <li className={'chat-msg is-first ' + (answering === right ? 'is-out' : 'is-in')}>
              <span className="chat-avatar">
                <AgentTile name={answering} size={32} />
              </span>
              <div className="chat-col">
                <Typing label={labelOf(answering) + ' is answering'} />
              </div>
            </li>
          )}
        </ol>
      </Scroller>
      <Composer
        key={data.id}
        threadId={data.id}
        messages={messages}
        me={me}
        everyone={agentsOf(overview.data)}
        replyTo={replyTo}
        setReplyTo={setReplyTo}
        onSent={() => {
          // Your own message should come into view.
          atBottom.current = true;
        }}
      />
    </section>
  );
}

/**
 * The open thread's title. In a thread you're in, clicking it renames it:
 * Enter or leaving the box saves, Escape doesn't.
 */
function ThreadTitle({ id, title, canRename }: { id: string; title: string; canRename: boolean }) {
  const client = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(title);
  const [error, setError] = useState('');
  const box = useRef<HTMLInputElement>(null);
  const done = useRef(false);

  useEffect(() => {
    if (editing) box.current?.select();
  }, [editing]);

  const rename = useMutation({
    mutationFn: (t: string) => api.renameThread(id, t),
    onSuccess: () => {
      setEditing(false);
      setError('');
      client.invalidateQueries({ queryKey: keys.thread(id) });
      client.invalidateQueries({ queryKey: keys.threads });
    },
    onError: (err) => {
      done.current = false;
      setError(err instanceof Error ? err.message : String(err));
    },
  });

  const save = () => {
    if (done.current) return;
    const t = draft.trim();
    if (!t || t === title) {
      setEditing(false);
      setError('');
      return;
    }
    done.current = true;
    rename.mutate(t);
  };

  if (editing) {
    return (
      <>
        <h2 id="thread-title" className="visually-hidden">
          {title}
        </h2>
        <form
          className="chat-title-edit"
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          <label htmlFor="thread-title-input" className="visually-hidden">
            Thread title
          </label>
          <input
            id="thread-title-input"
            ref={box}
            className="chat-title-input"
            value={draft}
            maxLength={100}
            disabled={rename.isPending}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? 'thread-title-error' : undefined}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={save}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.preventDefault();
                done.current = true;
                setEditing(false);
                setError('');
              }
            }}
          />
          {error && (
            <span id="thread-title-error" className="field-error" role="alert">
              Error: {error}
            </span>
          )}
        </form>
      </>
    );
  }
  if (!canRename) {
    return (
      <h2 id="thread-title" className="panel-title chat-title">
        {title}
      </h2>
    );
  }
  return (
    <h2 id="thread-title" className="panel-title chat-title">
      <button
        type="button"
        className="chat-title-button"
        title="Rename this thread"
        onClick={() => {
          done.current = false;
          setDraft(title);
          setEditing(true);
        }}
      >
        <span className="chat-title-text">{title}</span>
        <svg aria-hidden="true" viewBox="0 0 16 16" width="14" height="14" className="chat-title-pencil">
          <path d="M11.5 2.5l2 2L6 12l-3 1 1-3 7.5-7.5z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
        </svg>
        <span className="visually-hidden">, rename</span>
      </button>
    </h2>
  );
}

/** What you were writing in each thread, kept while you look at another one. It lasts until the page reloads. */
const drafts = new Map<string, string>();

/** Whether you can answer m from the reply box: you're in its conversation and it isn't a notice. */
function canReply(m: Tracked, me: string): boolean {
  return (m.from === me || m.to === me) && !isSystem(agentOf(m.from)) && !isSystem(agentOf(m.to));
}

/** The other side of m, as seen from you. */
function otherSide(m: Tracked, me: string): string {
  return m.from === me ? agentOf(m.to) : agentOf(m.from);
}

/** The session of `agent` in m, if m names one: the one that sent it, or the one that took or was meant to take it. */
function sessionIn(m: Tracked, agent: string): string {
  if (agentOf(m.from) === agent) return m.from_session || '';
  if (m.to === agent) return m.taken_session || m.to_session || '';
  return '';
}

/**
 * The session of `agent` a message in this thread goes to, as birdie routes it:
 * `want` (the session of the message being answered) while it runs, else the
 * latest of the agent's sessions in the thread that runs, else `want`, else the latest.
 */
function threadSession(messages: Tracked[], agent: string, running: string[], want = ''): string {
  if (want && running.includes(want)) return want;
  let latest = '';
  for (let i = messages.length - 1; i >= 0; i--) {
    const s = sessionIn(messages[i], agent);
    if (!s) continue;
    if (running.includes(s)) return s;
    latest ||= s;
  }
  return want || latest;
}

/** The message a reply to `agent` answers: the latest between you and it, so it reaches the same session. */
function replyTarget(messages: Tracked[], me: string, agent: string): Tracked | undefined {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if ((m.to === me && agentOf(m.from) === agent) || (m.from === me && agentOf(m.to) === agent)) return m;
  }
  return undefined;
}

/**
 * How an agent would get a message now. Interactive agents are online while a
 * session of theirs runs, and offline otherwise; their messages then wait.
 * Webhook and headless agents are reached whenever a message comes.
 */
function reachOf(a: Agent | undefined): { text: string; offline: boolean } {
  if (!a) return { text: '', offline: false };
  if (a.delivery === 'webhook') return { text: 'Webhook, answers in its own time', offline: false };
  if (a.delivery === 'acp') return { text: 'Starts when a message comes', offline: false };
  const live = liveSessions(a);
  if (!live.length) return { text: 'Offline, gets it when a session opens', offline: true };
  const s = defaultSession(a)?.session ?? live[0];
  const more = live.length > 1 ? ' +' + (live.length - 1) + ' more' : '';
  return { text: 'Online · ' + (s.name || 'a session') + (s.state ? ', ' + s.state : '') + more, offline: false };
}

function short(text: string, n = 80): string {
  const t = text.replace(/\s+/g, ' ').trim();
  return t.length > n ? t.slice(0, n - 1) + '…' : t;
}

/**
 * Writing into the open thread. It stays open, so the conversation can go on.
 * You can write to anyone in the thread, answer one message with its Reply, or
 * bring another agent in, from the To picker or by starting with @name.
 */
function Composer({
  threadId,
  messages,
  me,
  everyone,
  replyTo,
  setReplyTo,
  onSent,
}: {
  threadId: string;
  messages: Tracked[];
  me: string;
  everyone: Agent[];
  replyTo: Tracked | null;
  setReplyTo: (m: Tracked | null) => void;
  onSent: () => void;
}) {
  const client = useQueryClient();
  const byName = new Map(everyone.map((a) => [a.name, a]));
  const reach = (name: string) => reachOf(byName.get(name));
  // Offline agents go last in each group, keeping the order otherwise.
  const onlineFirst = (names: string[]) => [...names.filter((a) => !reach(a).offline), ...names.filter((a) => reach(a).offline)];
  const members = onlineFirst(agentsIn(messages.flatMap((m) => [m.from, m.to])).filter((a) => a !== me && !isSystem(a)));
  const outside = onlineFirst(everyone.map((a) => a.name).filter((a) => a !== me && !isSystem(a) && !members.includes(a)));
  // By default, answer whoever wrote to you last.
  let latest = '';
  for (let i = messages.length - 1; i >= 0 && !latest; i--) {
    const from = agentOf(messages[i].from);
    if (members.includes(from)) latest = from;
  }
  const [picked, setPicked] = useState('');
  // A session picked by hand, for an agent with more than one running.
  const [pickedSession, setPickedSession] = useState('');
  const [text, setText] = useState(() => drafts.get(threadId) ?? '');
  // The box as it is now, for a send that finishes after you've typed more.
  const latestText = useRef(text);
  latestText.current = text;
  const [error, setError] = useState('');
  const [said, setSaid] = useState('');
  const mention = useRef('');
  const box = useRef<HTMLTextAreaElement>(null);
  // Suggestions while you type @name: where the caret is, which one is active, and an @ you closed them for.
  const [caret, setCaret] = useState(0);
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState(-1);

  const known = (a: string) => members.includes(a) || outside.includes(a);
  const to = replyTo ? otherSide(replyTo, me) : known(picked) ? picked : latest || members[0] || '';
  const joining = !!to && !members.includes(to);

  useEffect(() => {
    if (box.current && takeFocus(threadId)) box.current.focus({ preventScroll: true });
  }, [threadId]);

  // A Reply goes to the session of the message it answers, not one picked before it.
  useEffect(() => setPickedSession(''), [replyTo?.id]);

  // Grow with what you write, up to a few lines; then it scrolls.
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 180) + 'px';
  }, [text]);

  const mutation = useMutation({
    mutationFn: api.send,
    onSuccess: (_, input) => {
      setError('');
      setReplyTo(null);
      setSaid('Sent to ' + labelOf(input.to) + '.');
      onSent();
      client.invalidateQueries({ queryKey: keys.thread(threadId) });
      client.invalidateQueries({ queryKey: keys.threads });
      client.invalidateQueries({ queryKey: keys.overview });
    },
    onError: (err, input) => {
      // The box was cleared when it was sent; put the message back, ahead of anything typed since.
      const now = latestText.current.trim();
      change(now ? input.content + '\n\n' + now : input.content);
      setSaid('');
      setError(err instanceof Error ? err.message : String(err));
    },
  });

  if (!messages.some((m) => m.from === me || m.to === me)) {
    return <p className="chat-compose-note">You're not in this thread, so you can't write in it. Only the agents in a thread can.</p>;
  }
  if (!to) return null;

  // The sessions you can choose between, and the one it would reach on its own:
  // the one in your exchange with that agent here, else the agent's default.
  // The overview loads apart from the thread, so the agent may not be known yet.
  const toAgent = byName.get(to);
  const sessions = toAgent?.delivery === 'pull' ? liveSessions(toAgent) : [];
  const parent = replyTo ?? (members.includes(to) ? replyTarget(messages, me, to) : undefined);
  const pinned = threadSession(messages, to, sessions.map((s) => s.id), parent ? sessionIn(parent, to) : '');
  const auto = sessions.find((s) => s.id === pinned) ?? (toAgent && defaultSession(toAgent)?.session) ?? sessions[0];
  const session = sessions.find((s) => s.id === pickedSession) ?? auto;

  const pick = (agent: string) => {
    setPicked(agent);
    if (agent !== to) setPickedSession('');
    setError('');
    // A Reply answers one agent; picking another one stops it.
    if (replyTo && otherSide(replyTo, me) !== agent) setReplyTo(null);
  };
  const change = (v: string, at = v.length) => {
    setText(v);
    setCaret(at);
    setActive(0);
    setError('');
    if (v) drafts.set(threadId, v);
    else drafts.delete(threadId);
    // Starting with @name picks who it goes to, once per mention, so the picker can still change it.
    const found = mentionIn(v, everyone.map((a) => ({ name: a.name, label: a.label || a.name })));
    if (found !== mention.current) {
      mention.current = found;
      if (found && !found.startsWith('?') && found !== me && !isSystem(found)) pick(found);
    }
  };
  const submit = (e?: FormEvent) => {
    e?.preventDefault();
    const content = text.trim();
    if (!content || mutation.isPending) return;
    // Name the session when you picked it, or it differs from where it would go anyway.
    const chosen = sessions.some((s) => s.id === pickedSession);
    const address = sessions.length > 1 && session && (chosen || session.id !== auto?.id) ? to + '/' + session.id : to;
    // Empty the box now, so what you type while it's on its way is the next message.
    setText('');
    drafts.delete(threadId);
    mention.current = '';
    mutation.mutate(parent ? { to: address, content, in_reply_to: parent.id } : { to: address, content, thread_id: threadId });
  };
  const query = mentionQuery(text, caret);
  const q = query ? query.q.toLowerCase() : '';
  const suggestions =
    query && query.start !== dismissed
      ? [...members, ...outside]
          .filter((a) => {
            const label = labelOf(a).toLowerCase();
            return a.toLowerCase().startsWith(q) || label.replace(/\s+/g, '').startsWith(q) || label.split(/\s+/).some((w) => w.startsWith(q));
          })
          .slice(0, 8)
      : [];
  const current = Math.min(active, suggestions.length - 1);
  // Puts "@name " in place of what you typed, and writes to that agent.
  const choose = (agent: string) => {
    if (!query) return;
    const before = text.slice(0, query.start) + '@' + agent + ' ';
    change(before + text.slice(caret).replace(/^[\p{L}\p{N}._-]*/u, ''), before.length);
    pick(agent);
    requestAnimationFrame(() => {
      box.current?.focus();
      box.current?.setSelectionRange(before.length, before.length);
    });
  };
  const keyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    const composing = e.nativeEvent.isComposing || e.keyCode === 229;
    if (suggestions.length && !composing) {
      const n = suggestions.length;
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        setActive((current + (e.key === 'ArrowDown' ? 1 : n - 1)) % n);
        return;
      }
      if ((e.key === 'Enter' && !e.shiftKey) || e.key === 'Tab') {
        e.preventDefault();
        choose(suggestions[current]);
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        setDismissed(query ? query.start : -1);
        return;
      }
    }
    // Not while an input method is composing a character (Safari reports that as keyCode 229).
    if (e.key === 'Enter' && !e.shiftKey && !composing) submit(e);
    else if (e.key === 'Escape' && replyTo) {
      e.preventDefault();
      setReplyTo(null);
    }
  };

  const unknown = mention.current.startsWith('?') ? mention.current.slice(1) : '';
  const option = (a: string, where: string) => {
    const r = reach(a);
    return { value: a, label: labelOf(a), hint: where + ' · ' + r.text, dim: r.offline, icon: <AgentTile name={a} size={24} /> };
  };
  const options = [...members.map((a) => option(a, 'In this thread')), ...outside.map((a) => option(a, 'Add to this thread'))];
  let hint = 'It goes to ' + labelOf(to) + (replyTo ? ', answering that message.' : ' in this thread.');
  if (joining) hint = labelOf(to) + ' joins this thread with this message, and can read everything said in it so far.';
  if (reach(to).offline) hint += ' ' + labelOf(to) + " has no session open, so it waits until one starts.";
  else if (sessions.length > 1 && session) hint = hint.replace(labelOf(to), labelOf(to) + ' · ' + (session.name || 'session ' + session.id.slice(0, 8)));

  return (
    <form className="chat-compose" onSubmit={submit} noValidate aria-label={'Write to ' + labelOf(to)}>
      <div className="visually-hidden" role="status" aria-live="polite">
        {said}
      </div>
      {error && (
        <p id="compose-error" className="chat-compose-error" role="alert">
          Error: {error}. Your message is still in the box.
        </p>
      )}
      {replyTo && (
        <div className="chat-compose-reply">
          <span className="chat-compose-reply-text">
            Replying to <strong>{who(replyTo.from === me ? replyTo.to_label : replyTo.from_label)}</strong>: “{short(replyTo.content)}”
          </span>
          <button type="button" className="chat-compose-reply-cancel" onClick={() => setReplyTo(null)} aria-label="Stop replying to that message">
            ✕
          </button>
        </div>
      )}
      <div className="chat-compose-to">
        <span id="compose-to-label">To</span>
        <Select id="compose-to" labelledBy="compose-to-label" value={to} onChange={pick} options={options} placement="up" />
        {sessions.length > 1 && session && (
          <>
            <span id="compose-session-label" className="visually-hidden">
              Session of {labelOf(to)}
            </span>
            <Select
              id="compose-session"
              labelledBy="compose-session-label"
              value={session.id}
              onChange={setPickedSession}
              placement="up"
              options={sessions.map((s) => ({
                value: s.id,
                // Two sessions can share a name, such as two windows in one folder; their ids tell them apart.
                label: !s.name ? 'Session ' + s.id.slice(0, 8) : sessions.filter((x) => x.name === s.name).length > 1 ? s.name + ' · ' + s.id.slice(0, 8) : s.name,
                hint: [s.id === auto?.id ? (pinned === s.id ? 'talking with you' : 'default') : '', s.state, s.branch, homePath(s.cwd)].filter(Boolean).join(' · '),
              }))}
            />
          </>
        )}
      </div>
      <div className="chat-compose-row">
        {suggestions.length > 0 && (
          <ul id="compose-mentions" role="listbox" aria-label="Agents to write to" className="dd-list mention-list">
            {suggestions.map((a, i) => (
              <li
                key={a}
                id={'compose-mention-' + i}
                role="option"
                aria-selected={i === current}
                className={'dd-option' + (i === current ? ' is-active' : '') + (reach(a).offline ? ' is-dim' : '')}
                onPointerDown={(e) => e.preventDefault()}
                onPointerMove={() => i !== current && setActive(i)}
                onClick={() => choose(a)}
              >
                <span className="dd-choice">
                  <span className="dd-icon">
                    <AgentTile name={a} size={24} />
                  </span>
                  <span className="dd-text">
                    <span className="dd-label">{labelOf(a)}</span>
                    <span className="dd-hint">
                      @{a} · {members.includes(a) ? 'In this thread' : 'Add to this thread'} · {reach(a).text}
                    </span>
                  </span>
                </span>
              </li>
            ))}
          </ul>
        )}
        <label htmlFor="compose-text" className="visually-hidden">
          Message to {labelOf(to)}
        </label>
        <textarea
          id="compose-text"
          ref={box}
          className="textarea chat-compose-text"
          rows={1}
          value={text}
          onChange={(e) => change(e.target.value, e.target.selectionStart)}
          onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
          onBlur={() => setDismissed(query ? query.start : -1)}
          onFocus={() => setDismissed(-1)}
          onKeyDown={keyDown}
          aria-autocomplete="list"
          aria-controls={suggestions.length ? 'compose-mentions' : undefined}
          aria-activedescendant={suggestions.length ? 'compose-mention-' + current : undefined}
          placeholder={'Write to ' + labelOf(to) + '…'}
          aria-invalid={error ? true : undefined}
          aria-describedby={(error ? 'compose-error ' : '') + 'compose-hint'}
        />
        <Button kind="primary" type="submit" disabled={!text.trim()}>
          {mutation.isPending ? 'Sending' : 'Send'}
        </Button>
      </div>
      <span id="compose-hint" className="chat-compose-hint">
        {unknown ? 'No agent called ' + unknown + ', so it still goes to ' + labelOf(to) + '. ' : ''}
        {hint} Enter sends, Shift+Enter starts a new line, @name picks someone else.
      </span>
    </form>
  );
}

function Bubble({
  m,
  day,
  out,
  starts,
  target,
  open,
  toggle,
  kind,
  now,
  reply,
}: {
  m: Tracked;
  day: string;
  out: boolean;
  starts: boolean;
  target: boolean;
  open: boolean;
  toggle: () => void;
  kind: Delivery | undefined;
  now: number;
  /** Answers this message from the reply box; unset when you can't. */
  reply?: () => void;
}) {
  const from = agentOf(m.from);
  const chip = chipFor(m.status, !!m.error);
  const detailsId = 'msg-details-' + m.id;
  return (
    <>
      {day && (
        <li className="chat-day">
          <span>{day}</span>
        </li>
      )}
      <li id={'msg-' + m.id} className={'chat-msg ' + (out ? 'is-out' : 'is-in') + (starts ? ' is-first' : '') + (target ? ' is-target' : '')}>
        <span className="chat-avatar">{starts && <AgentTile name={from} size={32} />}</span>
        <div className="chat-col">
          {starts && <span className="chat-from">{labelOf(from)}</span>}
          <div
            className="chat-bubble"
            onClick={() => {
              // Selecting text to copy shouldn't open the details.
              if (!window.getSelection()?.toString()) toggle();
            }}
          >
            {m.content}
          </div>
          <div className="chat-meta-row">
            <button type="button" className={'chat-meta' + (troubled(m) ? ' is-problem' : '')} aria-expanded={open} aria-controls={detailsId} onClick={toggle}>
              <time dateTime={m.created_at}>{hm(m.created_at)}</time>
              <span aria-hidden="true">·</span>
              <span>{chip.label}</span>
              <svg aria-hidden="true" viewBox="0 0 16 16" width="12" height="12" className="chat-meta-chevron">
                <path d="M3.5 6l4.5 4.5L12.5 6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
            {reply && (
              <button type="button" className="chat-reply" onClick={reply} aria-label={'Reply to this message from ' + labelOf(from)}>
                Reply
              </button>
            )}
          </div>
          {open && (
            <dl id={detailsId} className="chat-details">
              <div>
                <dt>Route</dt>
                <dd>
                  {who(m.from_label)} → {who(m.to_label)}
                </dd>
              </div>
              <div>
                <dt>Status</dt>
                <dd className={troubled(m) ? 'is-problem' : undefined}>
                  {progress(m, kind, now)}
                </dd>
              </div>
              <div>
                <dt>Sent</dt>
                <dd>{full(m.created_at)}</dd>
              </div>
              <div>
                <dt>Message</dt>
                <dd>{m.id}</dd>
              </div>
              {m.in_reply_to && (
                <div>
                  <dt>Answers</dt>
                  <dd>
                    <a href={'#msg-' + m.in_reply_to} onClick={(e) => jump(e, m.in_reply_to!)}>
                      {m.in_reply_to}
                    </a>
                  </dd>
                </div>
              )}
            </dl>
          )}
        </div>
      </li>
    </>
  );
}

/** Scrolls the thread to a message in it without changing the route. */
function jump(e: MouseEvent, id: string) {
  const el = document.getElementById('msg-' + id);
  if (!el) return;
  e.preventDefault();
  el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  el.classList.remove('is-flash');
  void el.offsetWidth;
  el.classList.add('is-flash');
}

function hm(iso: string): string {
  const t = ms(iso);
  return t === null ? '' : new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });
}

function full(iso: string): string {
  const t = ms(iso);
  return t === null ? '' : new Date(t).toLocaleString([], { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
}

/** "Today", "Yesterday", or a date, for the separators between days. */
function dayOf(iso: string, now: number): string {
  const t = ms(iso);
  if (t === null) return '';
  const d = new Date(t);
  const today = new Date(now);
  if (d.toDateString() === today.toDateString()) return 'Today';
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return 'Yesterday';
  return d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric', year: d.getFullYear() === today.getFullYear() ? undefined : 'numeric' });
}

function ProblemNote({ m }: { m: Tracked }) {
  if (m.status === 'failed') {
    return (
      <div className="thread-problem">
        <span className="error-label">Error:</span> birdie gave up on the message to <strong>{who(m.to_label)}</strong> after {m.attempts}{' '}
        {m.attempts === 1 ? 'try' : 'tries'}: {errorText(m.error)}. The sender was told.
      </div>
    );
  }
  return (
    <div className="thread-problem">
      <span className="error-label">Error:</span> the last try to reach <strong>{who(m.to_label)}</strong> failed: {errorText(m.error)}. birdie keeps
      trying, and tells the sender if it gives up after {MAX_TRIES} tries.
    </div>
  );
}
