import type { Session } from '../api/types';
import type { Chip } from '../lib/status';
import { ms, span } from '../lib/time';

export function StatusChip({ chip }: { chip: Chip }) {
  return <span className={'chip chip-' + chip.tone}>{chip.label}</span>;
}

/** What a session is doing, in the CLI's words: busy, idle 12m, needs you 3m, running, stopped 20m ago. */
export function sessionState(s: Session, now: number): { kind: 'busy' | 'idle' | 'waiting' | 'live' | 'stopped'; label: string } {
  const since = (iso: string | undefined | null) => {
    const t = ms(iso);
    return t === null ? '' : ' ' + span(now - t);
  };
  if (!s.live) return { kind: 'stopped', label: 'stopped' + (s.ended_at ? since(s.ended_at) + ' ago' : since(s.last_seen) + ' ago') };
  if (s.state === 'busy') return { kind: 'busy', label: 'busy' };
  if (s.state === 'idle') return { kind: 'idle', label: 'idle' + since(s.active_at) };
  if (s.state === 'waiting') return { kind: 'waiting', label: 'needs you' + since(s.active_at) };
  return { kind: 'live', label: 'running' };
}

export function SessionStateMark({ session, now }: { session: Session; now: number }) {
  const st = sessionState(session, now);
  return (
    <span className="state">
      <span aria-hidden="true" className={'state-mark state-' + st.kind + (st.kind === 'busy' ? ' dot-live' : '')} />
      {st.label}
    </span>
  );
}
