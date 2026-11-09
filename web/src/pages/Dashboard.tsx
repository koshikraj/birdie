import { useMemo } from 'react';
import { useOverview, useRecentMessages } from '../api/hooks';
import type { Agent, Overview, Session, Tracked } from '../api/types';
import { AgentTile } from '../components/AgentTile';
import { SessionStateMark, StatusChip } from '../components/bits';
import { agentOf, agentsOf, identity, liveSessions, sessionAddress } from '../lib/agents';
import { href } from '../lib/router';
import { MAX_TRIES, errorText, messageChip } from '../lib/status';
import { ago, agoWords, until, useNow } from '../lib/time';
import { Scene } from '../scene/Scene';

export function DashboardPage() {
  const overview = useOverview();
  const recent = useRecentMessages();
  const o = overview.data;
  const now = useNow();

  return (
    <>
      <div className="page-head">
        <h1 className="page-title">What your agents are up to</h1>
        <p className="page-lede">Every message on this machine, as it moves. An agent turns into its bird while it sends or gets a message.</p>
      </div>

      {o && <Attention overview={o} now={now} />}
      {o && <Stats overview={o} now={now} />}
      {overview.isError && !o && (
        <p className="panel" role="status">
          {String(overview.error?.message || "Couldn't load the overview.")}
        </p>
      )}

      <div className="row-wrap">
        <div className="scene-slot">
          <Scene overview={o} recent={recent.data?.messages} />
        </div>
        <Feed messages={recent.data?.messages} loading={recent.isPending && !recent.isError} failed={recent.isError} now={now} />
      </div>

      {o && (
        <div className="row-wrap">
          <Pairs overview={o} />
          <Sessions overview={o} now={now} />
        </div>
      )}
    </>
  );
}

function Attention({ overview, now }: { overview: Overview; now: number }) {
  const waiting: { agent: Agent; session: Session }[] = [];
  for (const a of agentsOf(overview)) for (const s of liveSessions(a)) if (s.state === 'waiting') waiting.push({ agent: a, session: s });
  const failing = agentsOf(overview).filter((a) => a.health.error);
  if (!waiting.length && !failing.length) return null;
  return (
    <section aria-labelledby="attention-title" className="attention">
      <h2 id="attention-title">Needs attention</h2>
      {waiting.map(({ agent, session }) => (
        <div key={session.id} className="attention-row">
          <span className="chip chip-attention">Needs your answer</span>
          <p>
            <span className="mono">{sessionAddress(session)}</span> stopped to ask you something {agoWords(session.active_at, now)}. Answer it in{' '}
            {identity(agent.name).product}, and its messages carry on.
          </p>
        </div>
      ))}
      {failing.map((a) => {
        const h = a.health;
        const retry = h.retry ? until(h.retry, now) : '';
        return (
          <div key={a.name} className="attention-row">
            <span className="chip chip-delivery">Delivery problem</span>
            <p>
              <span className="error-label">Error:</span> the last delivery to <span className="mono">{a.name}</span> failed {agoWords(h.error_at, now)}:{' '}
              {errorText(h.error)}.{' '}
              {h.retry
                ? (retry === 'now' ? 'The next try is due now.' : 'Next try ' + retry + '.') + ' birdie gives up after ' + MAX_TRIES + ' tries.'
                : 'birdie gave up on that message.'}
              {h.waiting > 1 ? ' ' + h.waiting + ' messages are waiting for it.' : ''}
            </p>
            {h.error_id && <a href={href.thread(h.error_id)}>Open thread</a>}
          </div>
        );
      })}
    </section>
  );
}

function Stats({ overview, now }: { overview: Overview; now: number }) {
  const agents = agentsOf(overview);
  const withSessions = agents.filter((a) => liveSessions(a).length > 0).length;
  const sessions = agents.flatMap(liveSessions);
  const needYou = sessions.filter((s) => s.state === 'waiting').length;
  const busy = sessions.filter((s) => s.state === 'busy').length;
  const queuedFor = agents.filter((a) => a.health.waiting > 0).map((a) => a.name + ' ' + a.health.waiting);
  const tiles = [
    {
      label: 'Agents',
      value: agents.length,
      sub: withSessions === 1 ? '1 has sessions running' : withSessions + ' have sessions running',
    },
    {
      label: 'Sessions running',
      value: sessions.length,
      sub: needYou ? needYou + (needYou === 1 ? ' needs your answer' : ' need your answer') : busy ? busy + ' busy' : 'None need you',
    },
    {
      label: 'Messages today',
      value: overview.today.sent,
      sub: overview.today.last_at ? 'Last one ' + ago(overview.today.last_at, now) : 'None yet',
    },
    {
      label: 'Queued for agents',
      value: overview.waiting.total,
      sub: queuedFor.length ? queuedFor.join(' · ') : 'Nothing waiting',
    },
  ];
  return (
    <section aria-label="Today">
      <dl className="stats">
        {tiles.map((t) => (
          <div key={t.label} className="stat">
            <dt>{t.label}</dt>
            <dd>
              <span className="stat-value">{t.value}</span>
              <span className="stat-sub">{t.sub}</span>
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function Feed({ messages, loading, failed, now }: { messages?: Tracked[]; loading: boolean; failed: boolean; now: number }) {
  const shown = (messages ?? []).slice(0, 6);
  return (
    <section aria-labelledby="feed-title" className="panel feed">
      <div className="panel-head">
        <h2 id="feed-title" className="panel-title">Live messages</h2>
        <a href={href.messages} className="panel-link">All messages</a>
      </div>
      {!shown.length ? (
        <p className="panel-empty">{loading ? 'Loading messages.' : failed ? "Messages can't load until birdie is reachable." : 'No messages yet. Send one from Agents, or ask one agent to message another.'}</p>
      ) : (
        <ol>
          {shown.map((m) => (
            <li key={m.id}>
              <div className="tile-pair">
                <AgentTile name={agentOf(m.from)} size={34} ring />
                <AgentTile name={agentOf(m.to)} size={34} ring />
              </div>
              <div className="feed-body">
                <div className="feed-meta">
                  <a className="feed-route" href={href.thread(m.thread_id || m.id)}>
                    {m.from_label} → {m.to_label}
                  </a>
                  <span className="feed-when">{ago(m.created_at, now)}</span>
                </div>
                <span className="feed-text">{m.content}</span>
                <StatusChip chip={messageChip(m, messages ?? [])} />
              </div>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

function Pairs({ overview }: { overview: Overview }) {
  const list = useMemo(() => [...overview.pairs].filter((p) => p.count > 0).sort((x, y) => y.count - x.count), [overview.pairs]);
  const most = list.length ? list[0].count : 1;
  return (
    <section aria-labelledby="links-title" className="panel" style={{ flex: '1 1 420px' }}>
      <h2 id="links-title" className="panel-title" style={{ marginBottom: 8 }}>Who talks to whom today</h2>
      {!list.length && <p className="panel-empty">No agents have talked to each other yet today.</p>}
      {list.map((p) => (
        <div key={p.a + '|' + p.b} className="pairs-row">
          <div className="tile-pair">
            <AgentTile name={p.a} size={30} ring />
            <AgentTile name={p.b} size={30} ring />
          </div>
          <span className="pairs-label">{p.a} ⇄ {p.b}</span>
          <div className="pairs-bar" aria-hidden="true">
            <span style={{ width: Math.round((p.count / most) * 100) + '%' }} />
          </div>
          <span className="pairs-count">{p.count} {p.count === 1 ? 'message' : 'messages'}</span>
        </div>
      ))}
    </section>
  );
}

const ORDER = { waiting: 0, busy: 1, '': 2, idle: 3 } as Record<string, number>;

function Sessions({ overview, now }: { overview: Overview; now: number }) {
  const sessions = agentsOf(overview)
    .flatMap(liveSessions)
    .sort((a, b) => (ORDER[a.state || ''] ?? 2) - (ORDER[b.state || ''] ?? 2) || sessionAddress(a).localeCompare(sessionAddress(b)));
  return (
    <section aria-labelledby="sessions-title" className="panel" style={{ flex: '1 1 420px' }}>
      <div className="panel-head">
        <h2 id="sessions-title" className="panel-title">Sessions</h2>
        <a href={href.agents} className="panel-link">All agents</a>
      </div>
      {!sessions.length && <p className="panel-empty">No sessions are running.</p>}
      {sessions.map((s) => (
        <div key={s.agent + '/' + s.id} className="session-row">
          <span className="session-name">
            {sessionAddress(s)}
            {s.default && <span className="muted"> · default</span>}
          </span>
          <span className="session-state">
            <SessionStateMark session={s} now={now} />
          </span>
          <span className="session-branch">{s.branch || 'no branch'}</span>
        </div>
      ))}
      <p className="panel-note">Running means the session is open, but its agent doesn't say whether it's busy. Only Claude Code reports busy, idle and needs you.</p>
    </section>
  );
}
