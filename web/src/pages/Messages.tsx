import { useEffect, useMemo } from 'react';
import { ApiFailure } from '../api/client';
import { useOverview, useThread, useThreads } from '../api/hooks';
import type { Delivery, Tracked } from '../api/types';
import { AgentTile } from '../components/AgentTile';
import { StatusChip } from '../components/bits';
import { agentOf } from '../lib/agents';
import { href } from '../lib/router';
import { MAX_TRIES, errorText, progress, threadChip, troubled } from '../lib/status';
import { ago, clock, useNow } from '../lib/time';
import { Signal } from '../components/Signal';

export function MessagesPage({ id }: { id?: string }) {
  const threads = useThreads();
  const list = useMemo(() => threads.data?.threads ?? [], [threads.data]);
  const selected = id || list[0]?.id;
  const thread = useThread(selected);
  const now = useNow();
  // The route may name a message; the thread it belongs to is the one to mark.
  const threadId = thread.data?.id || selected;

  return (
    <>
      <div className="page-head">
        <h1 className="page-title">Messages</h1>
        <p className="page-lede">Every conversation between your agents, and how far each message got.</p>
      </div>
      <div className="row-wrap row-wrap-top">
        <section aria-labelledby="threads-title" className="panel threads">
          <h2 id="threads-title" className="panel-title">Threads</h2>
          {!list.length &&
            (threads.isPending && !threads.isError ? (
              <div className="panel-empty"><Signal label="Loading threads" /></div>
            ) : (
              <p className="panel-empty">{threads.isError ? "Threads can't load until birdie is reachable." : 'No messages yet. Send one from Agents, and its thread shows up here.'}</p>
            ))}
          {list.map((t) => {
            const on = t.id === threadId;
            return (
              <a key={t.id} href={href.thread(t.id)} className="thread-item" aria-current={on ? 'true' : undefined}>
                <AgentTile name={agentOf(t.participants[0] || t.last_from)} size={36} />
                <span className="thread-item-body">
                  <span className="thread-item-top">
                    <span className="thread-item-between">{t.participants.join(' ⇄ ')}</span>
                    <span className="thread-item-when">{ago(t.last_at, now)}</span>
                  </span>
                  <span className="thread-item-preview">{t.last_text}</span>
                  <StatusChip chip={threadChip(t)} />
                </span>
              </a>
            );
          })}
        </section>
        <ThreadDetail id={selected} target={id} query={thread} now={now} />
      </div>
    </>
  );
}

function ThreadDetail({
  id,
  target,
  query,
  now,
}: {
  id?: string;
  target?: string;
  query: ReturnType<typeof useThread>;
  now: number;
}) {
  const overview = useOverview();
  const kinds = useMemo(() => {
    const m = new Map<string, Delivery>();
    for (const a of overview.data?.agents ?? []) m.set(a.name, a.delivery);
    return m;
  }, [overview.data]);

  const data = query.data;
  const messages = data?.messages ?? [];
  const who: string[] = [];
  for (const m of messages) for (const n of [m.from, m.to]) if (!who.includes(n)) who.push(n);

  useEffect(() => {
    if (!target || !data) return;
    document.getElementById('msg-' + target)?.scrollIntoView({ block: 'nearest' });
  }, [target, data]);

  if (!id) {
    return (
      <section aria-labelledby="thread-title" className="panel thread">
        <h2 id="thread-title" className="panel-title">No thread picked</h2>
        <p className="panel-empty">Pick a thread to see its messages.</p>
      </section>
    );
  }
  if (query.isError && !data) {
    const gone = query.error instanceof ApiFailure && query.error.status === 404;
    return (
      <section aria-labelledby="thread-title" className="panel thread">
        <h2 id="thread-title" className="panel-title">{gone ? "That thread isn't here" : "Couldn't load the thread"}</h2>
        <p className="panel-empty">{gone ? 'There is no thread or message with the id ' + id + '.' : String(query.error.message)}</p>
      </section>
    );
  }
  if (!data) {
    return (
      <section aria-labelledby="thread-title" className="panel thread" aria-busy="true">
        <h2 id="thread-title" className="visually-hidden">Loading the thread</h2>
        <Signal label="Loading the thread" />
      </section>
    );
  }

  const problems = messages.filter(troubled);
  return (
    <section aria-labelledby="thread-title" className="panel thread">
      <div className="thread-head">
        <h2 id="thread-title" className="panel-title">{who.join(' ⇄ ') || data.id}</h2>
        <span className="thread-id">
          {data.id} · {messages.length} {messages.length === 1 ? 'message' : 'messages'}
        </span>
      </div>

      {problems.map((m) => (
        <ProblemNote key={m.id} m={m} />
      ))}

      <ol className="thread-messages">
        {messages.map((m) => (
          <li key={m.id} id={'msg-' + m.id} className={target === m.id ? 'is-target' : undefined}>
            <div className="msg-meta">
              <AgentTile name={agentOf(m.from)} size={28} />
              <span className="msg-route">{m.from_label} → {m.to_label}</span>
              <span className="msg-when">
                <time dateTime={m.created_at}>{clock(m.created_at, now)}</time> · {m.id}
              </span>
            </div>
            <p className="msg-text">{m.content}</p>
            <span className={'msg-status' + (troubled(m) ? ' is-problem' : '')}>{progress(m, kinds.get(m.to), now)}</span>
          </li>
        ))}
      </ol>
      <p className="same-as">Same as: birdie thread {data.id}</p>
    </section>
  );
}

function ProblemNote({ m }: { m: Tracked }) {
  if (m.status === 'failed') {
    return (
      <div className="thread-problem">
        <span className="error-label">Error:</span> birdie gave up on the message to <span className="mono">{m.to_label}</span> after {m.attempts}{' '}
        {m.attempts === 1 ? 'try' : 'tries'}: {errorText(m.error)}. The sender was told.
      </div>
    );
  }
  return (
    <div className="thread-problem">
      <span className="error-label">Error:</span> the last try to reach <span className="mono">{m.to_label}</span> failed: {errorText(m.error)}. birdie keeps
      trying, and tells the sender if it gives up after {MAX_TRIES} tries.
    </div>
  );
}
