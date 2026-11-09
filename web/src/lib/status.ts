import type { Delivery, MessageStatus, ThreadSummary, Tracked } from '../api/types';
import { who } from './agents';
import { ago, ms, span } from './time';

/** birdie stops retrying a message after this many tries (internal/bus maxAttempts). */
export const MAX_TRIES = 8;

function plural(n: number, one: string, many: string) {
  return n === 1 ? one : many;
}

/** Makes a delivery error fit in a sentence, like the CLI does. */
export function errorText(s: string, n = 200): string {
  const t = s.trim().replace(/[\r\n\t]+/g, ' ').replace(/\.+$/, '');
  return t.length <= n ? t : t.slice(0, n - 1) + '…';
}

/** How far a message got, in the words `birdie thread` uses. */
export function progress(m: Tracked, kind: Delivery | undefined, now: number): string {
  switch (m.status) {
    case 'queued': {
      if (!m.error) return 'queued';
      let s = 'queued; try ' + m.attempts + ' failed ' + ago(m.error_at, now) + ': ' + errorText(m.error);
      const retry = ms(m.retry);
      if (retry !== null) s += retry > now ? '; next try in ' + span(retry - now) : '; next try is due';
      return s;
    }
    case 'claimed':
      if (kind === 'acp') return 'being answered';
      return m.taken_by ? 'taken by ' + who(m.taken_by) + ', not confirmed yet' : 'taken, not confirmed yet';
    case 'delivered':
    case 'replied': {
      let s = m.status === 'replied' ? 'answered; delivered' : 'delivered';
      const d = ms(m.delivered_at);
      const c = ms(m.created_at);
      if (d !== null && c !== null) s += ' after ' + span(d - c);
      if (m.taken_by) s += ' to ' + who(m.taken_by);
      return s;
    }
    case 'failed':
      return 'not delivered: birdie gave up after ' + m.attempts + ' ' + plural(m.attempts, 'try', 'tries') + ': ' + errorText(m.error);
  }
  return m.status;
}

export type ChipTone = 'done' | 'answered' | 'problem' | 'pending';

export interface Chip {
  label: string;
  tone: ChipTone;
}

/** The short status chip for a message in a list. */
export function chipFor(status: MessageStatus, hasError: boolean, answeredBy?: string): Chip {
  switch (status) {
    case 'replied':
      return { label: answeredBy ? 'answered by ' + answeredBy : 'answered', tone: 'answered' };
    case 'delivered':
      return { label: 'delivered', tone: 'done' };
    case 'claimed':
      return { label: 'taken', tone: 'pending' };
    case 'failed':
      return { label: 'not delivered', tone: 'problem' };
    case 'queued':
      return hasError ? { label: 'retrying', tone: 'problem' } : { label: 'queued', tone: 'pending' };
  }
  return { label: status, tone: 'pending' };
}

export function messageChip(m: Tracked, all: Tracked[]): Chip {
  const reply = m.status === 'replied' ? all.find((x) => x.in_reply_to === m.id) : undefined;
  return chipFor(m.status, !!m.error, reply?.from_label && who(reply.from_label));
}

export function threadChip(t: ThreadSummary): Chip {
  if (t.problem && t.status !== 'failed') return { label: 'retrying', tone: 'problem' };
  return chipFor(t.status, t.problem);
}

/** A message has a delivery problem worth flagging. */
export function troubled(m: Tracked): boolean {
  return m.status === 'failed' || (m.status === 'queued' && !!m.error);
}
