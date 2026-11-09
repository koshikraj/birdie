import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type MouseEvent, type RefObject } from 'react';
import { ApiFailure } from '../api/client';
import { useOverview, useThread, useThreads } from '../api/hooks';
import type { Delivery, ThreadSummary, Tracked } from '../api/types';
import { AgentTile } from '../components/AgentTile';
import { StatusChip } from '../components/bits';
import { Scroller } from '../components/Scroller';
import { Loading, Skeleton, Typing } from '../components/Signal';
import { TilePair } from '../components/TilePair';
import { ME, agentOf, labelOf, who } from '../lib/agents';
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
      <TilePair a={agents[0]} b={agents[1]} size={30} />
      <span className="thread-item-body">
        <span className="thread-item-top">
          <span className="thread-item-between">{agents.map(labelOf).join(' ⇄ ')}</span>
          <span className="thread-item-when">{ago(t.last_at, now)}</span>
        </span>
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
        {agents.length > 0 && <TilePair a={agents[0]} b={agents[1]} size={38} />}
        <div className="chat-head-text">
          <h2 id="thread-title" className="panel-title">{agents.map(labelOf).join(' ⇄ ') || data.id}</h2>
          <span className="chat-sub">
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
    </section>
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
          <button type="button" className={'chat-meta' + (troubled(m) ? ' is-problem' : '')} aria-expanded={open} aria-controls={detailsId} onClick={toggle}>
            <time dateTime={m.created_at}>{hm(m.created_at)}</time>
            <span aria-hidden="true">·</span>
            <span>{chip.label}</span>
            <svg aria-hidden="true" viewBox="0 0 16 16" width="12" height="12" className="chat-meta-chevron">
              <path d="M3.5 6l4.5 4.5L12.5 6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </button>
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
