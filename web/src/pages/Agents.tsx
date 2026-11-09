import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type RefObject } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';
import { useBusEvent } from '../api/events';
import { keys, useOverview, useThread, useTools } from '../api/hooks';
import type { Agent, MessageStatus, Overview, SendResult, Tool } from '../api/types';
import { AgentTile } from '../components/AgentTile';
import { BirdShape } from '../components/BirdShape';
import { SessionStateMark } from '../components/bits';
import { Birds, Button, InstallCommand, Letter, TextField } from '../ds';
import {
  agentsOf,
  defaultSession,
  deliveryLabel,
  findAgent,
  homePath,
  identity,
  liveSessions,
  peersOf,
  sessionAddress,
} from '../lib/agents';
import { href } from '../lib/router';
import { errorText } from '../lib/status';
import { ago, ms, span, until, useNow } from '../lib/time';

export function AgentsPage({ section }: { section?: 'send' | 'add' }) {
  const overview = useOverview();
  const o = overview.data;
  const now = useNow();
  const [to, setTo] = useState('');
  const [picked, setPicked] = useState('');
  const [showStopped, setShowStopped] = useState(false);
  const textRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (!section) return;
    const el = document.getElementById(section);
    el?.scrollIntoView({ block: 'start' });
    if (section === 'send') document.getElementById('send-to')?.focus({ preventScroll: true });
  }, [section, !!o]);

  const pick = (name: string) => {
    setTo(name);
    setPicked(name);
    document.getElementById('send')?.scrollIntoView({ block: 'start' });
    textRef.current?.focus({ preventScroll: true });
  };

  const agents = agentsOf(o);
  const stopped = agents.reduce((n, a) => n + a.sessions.filter((s) => !s.live).length, 0);
  const peers = useMemo(() => peersOf(o?.pairs ?? []), [o]);

  return (
    <>
      <div className="page-head-row">
        <div className="page-head">
          <h1 className="page-title">Agents</h1>
          <p className="page-lede">Everyone birdie can reach on this machine, and what each of their sessions is doing.</p>
        </div>
        <div className="actions">
          <Button kind="secondary" href={href.send}>Send a message</Button>
          <Button kind="primary" href={href.add}>Add an agent</Button>
        </div>
      </div>

      <div className="row-wrap row-wrap-top">
        <div className="agents-list">
          {!o && <p className="panel">{overview.isError ? String(overview.error.message) : 'Loading agents.'}</p>}
          {o && !agents.length && (
            <div className="panel">
              <h2 className="panel-title">No agents yet</h2>
              <p className="agent-card-desc">Run this in a terminal to connect the agents on this machine:</p>
              <InstallCommand command="birdie onboard" />
            </div>
          )}
          {agents.map((a) => (
            <AgentCard
              key={a.name}
              agent={a}
              me={o?.me}
              now={now}
              peers={peers.get(a.name) || []}
              picked={picked === a.name}
              showStopped={showStopped}
              onPick={() => pick(a.name)}
            />
          ))}
          {stopped > 0 && (
            <p className="stopped-toggle">
              {stopped} {stopped === 1 ? 'session' : 'sessions'} stopped in the last day.
              <button type="button" className="link-button" aria-pressed={showStopped} onClick={() => setShowStopped((v) => !v)}>
                {showStopped ? 'Hide stopped sessions' : 'Show stopped sessions'}
              </button>
            </p>
          )}
        </div>

        <aside className="agents-aside" aria-label="Add an agent or send a message">
          <AddAgent />
          <SendForm
            overview={o}
            to={to}
            setTo={(v) => {
              setTo(v);
              setPicked(v.split('/')[0]);
            }}
            textRef={textRef}
          />
        </aside>
      </div>
    </>
  );
}

function AgentCard({
  agent: a,
  me,
  now,
  peers,
  picked,
  showStopped,
  onPick,
}: {
  agent: Agent;
  me?: string;
  now: number;
  peers: string[];
  picked: boolean;
  showStopped: boolean;
  onPick: () => void;
}) {
  const id = identity(a.name);
  const h = a.health;
  const live = liveSessions(a);
  const sessions = showStopped ? a.sessions : live;
  const headingId = 'agent-' + a.name.replace(/[^a-zA-Z0-9_-]/g, '-');
  const known = id.product !== a.name;
  const description =
    a.description || (a.name === me ? 'You, in this web app. Messages you send here come from ' + me + '.' : known ? id.product + ' on this machine.' : '');
  const oldest = ms(h.oldest);
  return (
    <article aria-labelledby={headingId} className={'agent-card' + (picked ? ' is-picked' : '') + (h.error ? ' is-problem' : '')}>
      <div className="agent-card-top">
        <div className="agent-stage">
          <BirdShape spec={id.spec} color={id.color} className="agent-stage-bird" />
          <AgentTile name={a.name} size={44} className="agent-badge" />
        </div>
        <div className="agent-card-name">
          <div>
            <h2 id={headingId}>{a.name}</h2>
            <span className="chip chip-done">{deliveryLabel(a.delivery)}</span>
          </div>
          {description && <p className="agent-card-desc">{description}</p>}
        </div>
        {a.name !== me && (
          <button type="button" className="ghost-button" aria-pressed={picked} onClick={onPick}>
            Message {a.name}
          </button>
        )}
      </div>

      <dl className="agent-facts">
        <div>
          <dt>Sessions running</dt>
          <dd>{a.delivery === 'pull' ? String(live.length) : 'none needed'}</dd>
        </div>
        <div>
          <dt>Queued for it</dt>
          <dd>{h.waiting ? h.waiting + (oldest !== null ? ', oldest ' + span(now - oldest) : '') : 'none'}</dd>
        </div>
        <div>
          <dt>Last delivered</dt>
          <dd>{ago(h.delivered, now)}</dd>
        </div>
        <div>
          <dt>Talks to</dt>
          <dd>{peers.length ? peers.join(', ') : 'nobody today'}</dd>
        </div>
      </dl>

      {sessions.length > 0 && (
        <div className="sessions-table-wrap">
          <table className="sessions-table">
            <caption className="visually-hidden">Sessions of {a.name}</caption>
            <thead>
              <tr>
                <th scope="col">Session</th>
                <th scope="col">State</th>
                <th scope="col">Branch</th>
                <th scope="col">Folder</th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.id} className={s.live ? undefined : 'is-stopped'}>
                  <td>
                    {sessionAddress(s)}
                    {s.default && s.live && <span className="muted"> · default</span>}
                  </td>
                  <td className="nowrap">
                    <SessionStateMark session={s} now={now} />
                  </td>
                  <td className="nowrap muted">{s.branch || 'no branch'}</td>
                  <td className="muted">{homePath(s.cwd) || 'unknown'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {h.error && (
        <div className="agent-problem">
          <p>
            Error: the last delivery failed {ago(h.error_at, now)}: {errorText(h.error)}.{' '}
            {h.retry ? (until(h.retry, now) === 'now' ? 'The next try is due.' : 'Next try ' + until(h.retry, now) + '.') : 'birdie gave up on that message.'}
            {h.gave_up > 0 ? ' ' + h.gave_up + (h.gave_up === 1 ? ' message' : ' messages') + ' given up on in the last day.' : ''}
          </p>
          {h.error_id && <a href={href.thread(h.error_id)}>Open thread</a>}
        </div>
      )}
    </article>
  );
}

/* Adding an agent: the web app builds the command, and you run it. */

const TABS = [
  { id: 'tool', label: 'A tool here' },
  { id: 'webhook', label: 'A bot with a webhook' },
  { id: 'headless', label: 'Headless' },
] as const;
type TabId = (typeof TABS)[number]['id'];

function AddAgent() {
  const [tab, setTab] = useState<TabId>('tool');
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const onKey = (e: KeyboardEvent<HTMLButtonElement>, i: number) => {
    let next = -1;
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') next = (i + 1) % TABS.length;
    else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') next = (i - 1 + TABS.length) % TABS.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = TABS.length - 1;
    if (next < 0) return;
    e.preventDefault();
    setTab(TABS[next].id);
    refs.current[next]?.focus();
  };
  return (
    <section id="add" aria-labelledby="add-title" className="add">
      <div className="add-stage">
        <h2 id="add-title" className="title">Add an agent</h2>
        <p>Pick how it gets its messages.</p>
        <div className="add-birds">
          <Birds pose="loop" stage={false} />
        </div>
      </div>
      <div role="tablist" aria-label="Kind of agent" className="tabs">
        {TABS.map((t, i) => (
          <button
            key={t.id}
            ref={(el) => {
              refs.current[i] = el;
            }}
            id={'tab-' + t.id}
            type="button"
            role="tab"
            className="tab"
            aria-selected={tab === t.id}
            aria-controls={'panel-' + t.id}
            tabIndex={tab === t.id ? 0 : -1}
            onClick={() => setTab(t.id)}
            onKeyDown={(e) => onKey(e, i)}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={'panel-' + tab} aria-labelledby={'tab-' + tab} className="tabpanel" tabIndex={0}>
        {tab === 'tool' && <ToolPanel />}
        {tab === 'webhook' && <WebhookPanel />}
        {tab === 'headless' && <HeadlessPanel />}
      </div>
    </section>
  );
}

function ToolPanel() {
  const tools = useTools(true);
  const list = tools.data?.tools ?? [];
  const missing = list.filter((t) => t.found && !t.connected);
  return (
    <>
      <p>birdie finds Claude Code, Codex, Grok CLI and Hermes on your PATH and connects them. Restart their sessions afterwards.</p>
      <div>
        {tools.isPending && <p className="panel-empty">Looking for tools on this machine.</p>}
        {tools.isError && <p className="panel-empty error-text">Error: {String(tools.error.message)}</p>}
        {list.map((t) => (
          <ToolRow key={t.name} tool={t} />
        ))}
      </div>
      {missing.length > 1 && <InstallCommand command="birdie setup --all" />}
      <p className="same-as">The web app doesn't run these. Copy a command and run it in a terminal.</p>
    </>
  );
}

function ToolRow({ tool: t }: { tool: Tool }) {
  const state = t.connected ? 'connected' : t.found ? 'found, not connected' : 'not found';
  return (
    <div className="tool-row">
      <AgentTile name={t.name} size={32} />
      <span className="tool-name">
        <span>{t.name}</span>
        <span>{t.product}</span>
      </span>
      <span className={'tool-state' + (t.connected ? ' is-ok' : '')}>{state}</span>
      {t.found && !t.connected && t.setup && <InstallCommand key={t.setup} command={t.setup} />}
    </div>
  );
}

const NAME_RE = /^[a-z0-9][a-z0-9.-]*$/;

/** Quotes a value for a POSIX shell when it needs it. A leading ~ stays bare so it expands. */
function shellArg(v: string): string {
  if (/^[A-Za-z0-9_@%+=:,./~-]+$/.test(v)) return v;
  if (v.startsWith('~/') && /^[A-Za-z0-9_@%+=:,./~-]+$/.test(v.slice(2).replace(/ /g, ''))) {
    return '~/' + "'" + v.slice(2).replace(/'/g, "'\\''") + "'";
  }
  return "'" + v.replace(/'/g, "'\\''") + "'";
}

/** Always quoted, the way the docs write --acp "COMMAND". */
function quotedArg(v: string): string {
  if (!/["$`\\]/.test(v)) return '"' + v + '"';
  return "'" + v.replace(/'/g, "'\\''") + "'";
}

function nameError(name: string): string | undefined {
  if (!name) return undefined;
  return NAME_RE.test(name) ? undefined : 'use lowercase letters, digits, dots and dashes, starting with a letter or digit';
}

function CommandOut({ command, ready }: { command: string; ready: boolean }) {
  return (
    <>
      <InstallCommand key={command} command={command} />
      <p className="same-as" role="status">
        {ready ? 'Run this in a terminal to add the agent. The web app shows the command but never runs it.' : 'Fill in the fields above to finish the command.'}
      </p>
    </>
  );
}

function WebhookPanel() {
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [file, setFile] = useState('');
  const err = nameError(name.trim());
  const command =
    'birdie agent add ' + (name.trim() && !err ? name.trim() : 'NAME') +
    ' --webhook ' + (url.trim() ? shellArg(url.trim()) : 'URL') +
    ' --auth-file ' + (file.trim() ? shellArg(file.trim()) : 'PATH');
  return (
    <>
      <p>For a bot that runs somewhere else, such as Grok Bot. birdie posts each message to its webhook and the bot answers with its token.</p>
      <TextField label="Name" mono placeholder="grok-bot" hint="Lowercase letters, digits, dots, dashes" error={err} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" spellCheck={false} />
      <TextField label="Webhook URL" mono placeholder="https://…" type="url" value={url} onChange={(e) => setUrl(e.target.value)} autoComplete="off" spellCheck={false} />
      <TextField
        label="Secret file"
        mono
        placeholder="~/.config/grok-bot/sender-key"
        hint="birdie reads the secret from this file; it never shows it"
        value={file}
        onChange={(e) => setFile(e.target.value)}
        autoComplete="off"
        spellCheck={false}
      />
      <CommandOut command={command} ready={!!(name.trim() && !err && url.trim() && file.trim())} />
    </>
  );
}

function HeadlessPanel() {
  const [name, setName] = useState('');
  const [cmd, setCmd] = useState('');
  const [dir, setDir] = useState('');
  const err = nameError(name.trim());
  const command =
    'birdie agent add ' + (name.trim() && !err ? name.trim() : 'NAME') +
    ' --acp ' + (cmd.trim() ? quotedArg(cmd.trim()) : '"COMMAND"') +
    (dir.trim() ? ' --cwd ' + shellArg(dir.trim()) : ' --cwd DIR');
  return (
    <>
      <p>No window needs to be open. For each message, birdie starts the agent, waits for its answer and posts it back.</p>
      <TextField label="Name" mono placeholder="grok" hint="Lowercase letters, digits, dots, dashes" error={err} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" spellCheck={false} />
      <TextField label="Command" mono placeholder="grok agent stdio" value={cmd} onChange={(e) => setCmd(e.target.value)} autoComplete="off" spellCheck={false} />
      <TextField label="Working folder" mono placeholder="~/grok-work" hint="Leave it out of the command to use your home folder" value={dir} onChange={(e) => setDir(e.target.value)} autoComplete="off" spellCheck={false} />
      <CommandOut command={command} ready={!!(name.trim() && !err && cmd.trim())} />
    </>
  );
}

/* Sending a message as the web app's own agent. */

interface Target {
  value: string;
  label: string;
}

function targetsOf(o: Overview | undefined): Target[] {
  const out: Target[] = [];
  for (const a of agentsOf(o)) {
    if (a.name === o?.me) continue;
    const def = defaultSession(a);
    out.push({ value: a.name, label: a.name + (def && liveSessions(a).length > 1 ? ' (its default session)' : '') });
    for (const s of liveSessions(a)) {
      // The bare name already reaches the default session.
      if (def && def.session.id === s.id) continue;
      out.push({ value: sessionAddress(s), label: sessionAddress(s) });
    }
  }
  return out;
}

/** Where a message to `to` goes, and how soon it's seen. */
function resolve(o: Overview | undefined, to: string): string {
  if (!o || !to) return '';
  const [name, sess] = [to.split('/')[0], to.includes('/') ? to.slice(to.indexOf('/') + 1) : ''];
  const a = findAgent(o, name);
  if (!a) return 'birdie holds it until an agent called ' + name + ' exists.';
  let s: string;
  let waiting = false;
  if (sess) {
    const found = a.sessions.find((x) => sessionAddress(x) === to || x.id === sess || x.name === sess);
    if (found && found.live) {
      s = 'Goes to ' + to + ', this session.';
      waiting = found.state === 'waiting';
    } else s = to + " isn't running, so it waits until that session comes back.";
  } else if (a.delivery === 'webhook') {
    s = "Goes to " + name + "'s webhook.";
  } else if (a.delivery === 'acp') {
    s = 'birdie starts ' + name + ' for it, waits for the answer and posts it back.';
  } else {
    const def = defaultSession(a);
    if (def) {
      s = 'Goes to ' + sessionAddress(def.session) + ', ' + def.why + '.';
      waiting = def.session.state === 'waiting';
    } else s = 'No session of ' + name + ' is running, so it waits until one starts.';
  }
  if (waiting) s += ' That session is waiting for your answer, so it sees this once you answer it.';
  if (a.health.error) s += ' Its deliveries are failing right now (' + errorText(a.health.error, 80) + '), so it may wait while birdie retries.';
  return s;
}

type Phase = MessageStatus | 'retrying';
const RANK: Record<Phase, number> = { queued: 0, retrying: 1, claimed: 2, delivered: 3, replied: 4, failed: 5 };

interface Sent {
  result: SendResult;
  text: string;
  at: number;
  phase: Phase;
  toLabel: string;
  deliveredAt: number | null;
  error: string;
  answeredBy: string;
}

function SendForm({
  overview: o,
  to,
  setTo,
  textRef,
}: {
  overview: Overview | undefined;
  to: string;
  setTo: (v: string) => void;
  textRef: RefObject<HTMLTextAreaElement>;
}) {
  const client = useQueryClient();
  const targets = useMemo(() => targetsOf(o), [o]);
  const [text, setText] = useState('');
  const [invalid, setInvalid] = useState(false);
  const [sent, setSent] = useState<Sent | null>(null);
  const [sendError, setSendError] = useState('');
  const now = useNow();

  // Fall back to the first agent when nothing, or something gone, is picked.
  const value = targets.some((t) => t.value === to) ? to : to && to.includes('/') ? to : targets[0]?.value || '';
  const options = targets.some((t) => t.value === value) || !value ? targets : [...targets, { value, label: value + " (not running)" }];

  const mutation = useMutation({
    mutationFn: api.send,
    onSuccess: (result, input) => {
      setSent({
        result,
        text: input.content,
        at: Date.now(),
        phase: result.status,
        toLabel: result.to_session ? sessionLabel(o, result.to, result.to_session) : result.to,
        deliveredAt: null,
        error: '',
        answeredBy: '',
      });
      setSendError('');
      setText('');
      client.invalidateQueries({ queryKey: keys.overview });
    },
    onError: (err) => {
      setSendError(err instanceof Error ? err.message : String(err));
    },
  });

  // Follow the message through the stream: queued, delivered, answered.
  useBusEvent((e) => {
    if (e.kind !== 'message' || !sent) return;
    if (e.id === sent.result.id) {
      const phase: Phase = e.type;
      setSent((s) => {
        if (!s || s.result.id !== e.id) return s;
        const next = { ...s, toLabel: e.to_label || s.toLabel };
        if (e.type === 'delivered' || e.type === 'replied') next.deliveredAt = next.deliveredAt ?? (ms(e.at) || Date.now());
        if (e.error) next.error = e.error;
        if (RANK[phase] >= RANK[s.phase] || (s.phase === 'retrying' && phase !== 'queued')) next.phase = phase;
        return next;
      });
    } else if (e.in_reply_to === sent.result.id && e.type === 'queued') {
      setSent((s) => (s ? { ...s, phase: 'replied', answeredBy: e.from_label || e.from } : s));
    }
  });

  // The thread is the record: it catches up if an event was missed.
  const thread = useThread(sent?.result.id);
  const tracked = thread.data?.messages.find((m) => m.id === sent?.result.id);
  const reply = thread.data?.messages.find((m) => m.in_reply_to === sent?.result.id);
  let phase: Phase = sent?.phase ?? 'queued';
  if (tracked) {
    const tp: Phase = tracked.status === 'queued' && tracked.error ? 'retrying' : tracked.status;
    if (RANK[tp] > RANK[phase]) phase = tp;
  }
  if (reply && phase !== 'failed') phase = 'replied';

  const submit = (e: { preventDefault(): void }) => {
    e.preventDefault();
    if (!text.trim()) {
      setInvalid(true);
      textRef.current?.focus();
      return;
    }
    if (!value) return;
    setInvalid(false);
    mutation.mutate({ to: value, content: text.trim() });
  };

  let title = '';
  let detail = '';
  let problem = false;
  if (sent) {
    const where = tracked?.taken_by || sent.toLabel;
    const deliveredAt = ms(tracked?.delivered_at) ?? sent.deliveredAt;
    const createdAt = ms(tracked?.created_at) ?? sent.at;
    const error = tracked?.error || sent.error;
    switch (phase) {
      case 'queued':
        title = 'Queued for ' + sent.toLabel;
        detail = sent.result.note;
        break;
      case 'retrying':
        problem = true;
        title = 'Still queued for ' + sent.toLabel;
        detail =
          (tracked?.attempts ? 'Try ' + tracked.attempts + ' failed' : 'A try failed') + (error ? ': ' + errorText(error) : '') + '.' +
          (tracked?.retry && until(tracked.retry, now) !== 'now' ? ' Next try ' + until(tracked.retry, now) + '.' : ' birdie tries again.');
        break;
      case 'claimed':
        title = 'Taken by ' + where + ', not confirmed yet';
        detail = 'Waiting for ' + where + ' to confirm it got the message.';
        break;
      case 'delivered':
        title = 'Delivered to ' + where + (deliveredAt ? ' after ' + span(deliveredAt - createdAt) : '');
        detail = 'Waiting for an answer. It will appear in Messages.';
        break;
      case 'replied':
        title = 'Answered by ' + (reply?.from_label || sent.answeredBy || where);
        detail = 'The answer is in the thread.';
        break;
      case 'failed':
        problem = true;
        title = 'Not delivered';
        detail = 'birdie gave up' + (error ? ': ' + errorText(error) : '') + '.';
        break;
    }
  }

  return (
    <section id="send" aria-labelledby="send-title" className="send">
      <div className="send-head">
        <h2 id="send-title" className="title">Send a message</h2>
        <p>
          You send as <span className="mono">{o?.me || 'me'}</span>. Answers come back to Messages.
        </p>
      </div>
      <form onSubmit={submit} noValidate className="send-form">
        <div className="field">
          <label htmlFor="send-to">To</label>
          <select id="send-to" className="select" aria-describedby="send-to-note" value={value} onChange={(e) => setTo(e.target.value)} disabled={!options.length}>
            {!options.length && <option value="">No agents yet</option>}
            {options.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
          <span id="send-to-note" className="field-note">{resolve(o, value)}</span>
        </div>
        <div className="field">
          <label htmlFor="send-text">Message</label>
          <textarea
            id="send-text"
            ref={textRef}
            className="textarea"
            rows={4}
            aria-invalid={invalid}
            aria-describedby={invalid ? 'send-text-error' : undefined}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              if (invalid) setInvalid(false);
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) submit(e);
            }}
            placeholder="Ask for something the other agent can do on its own."
          />
          {invalid && (
            <span id="send-text-error" className="field-error">
              Error: write a message first.
            </span>
          )}
        </div>
        <div className="send-row">
          <Button kind="primary" type="submit" disabled={mutation.isPending || !value}>
            {mutation.isPending ? 'Sending' : 'Send'}
          </Button>
          <span className="same-as">Same as: birdie send --as {o?.me || 'me'} --to {value || 'NAME'} "…"</span>
        </div>
      </form>
      <div role="status" aria-live="polite">
        {sendError && (
          <div className="sent is-problem">
            <Letter tone="request" size={28} />
            <div className="sent-body">
              <span className="sent-title">Error: the message wasn't sent</span>
              <span className="sent-detail">{sendError}. Your message is still in the box, so you can try again.</span>
            </div>
          </div>
        )}
        {sent && !sendError && (
          <div className={'sent' + (problem ? ' is-problem' : '')}>
            <Letter tone="request" size={28} />
            <div className="sent-body">
              <span className="sent-title">{title}</span>
              <span className="sent-text">“{sent.text.length > 160 ? sent.text.slice(0, 159) + '…' : sent.text}”</span>
              {detail && <span className="sent-detail">{detail}</span>}
              <a href={href.thread(sent.result.thread_id || sent.result.id)}>Open thread</a>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

function sessionLabel(o: Overview | undefined, agent: string, session: string): string {
  const a = findAgent(o, agent);
  const s = a?.sessions.find((x) => x.id === session || x.name === session);
  return s ? sessionAddress(s) : agent + '/' + session.slice(0, 8);
}
