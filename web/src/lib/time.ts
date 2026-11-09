import { useSyncExternalStore } from 'react';

/** Milliseconds since the epoch for an RFC 3339 time, or null. */
export function ms(iso: string | null | undefined): number | null {
  if (!iso) return null;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? null : t;
}

/** Roughly how long d milliseconds is: 40s, 12m, 3h, 2d. Matches the CLI. */
export function span(d: number): string {
  const sec = Math.max(0, Math.floor(d / 1000));
  if (sec < 60) return sec + 's';
  if (sec < 3600) return Math.floor(sec / 60) + 'm';
  if (sec < 48 * 3600) return Math.floor(sec / 3600) + 'h';
  return Math.floor(sec / 86400) + 'd';
}

/** "just now", "12s ago", "3m ago", or "never". */
export function ago(iso: string | null | undefined, now: number): string {
  const t = ms(iso);
  if (t === null) return 'never';
  if (now - t < 5000) return 'just now';
  return span(now - t) + ' ago';
}

/** "3 minutes ago", for running sentences. */
export function agoWords(iso: string | null | undefined, now: number): string {
  const t = ms(iso);
  if (t === null) return 'at some point';
  const sec = Math.max(0, Math.floor((now - t) / 1000));
  const unit = (n: number, one: string) => n + ' ' + (n === 1 ? one : one + 's') + ' ago';
  if (sec < 10) return 'just now';
  if (sec < 60) return unit(sec, 'second');
  if (sec < 3600) return unit(Math.floor(sec / 60), 'minute');
  if (sec < 48 * 3600) return unit(Math.floor(sec / 3600), 'hour');
  return unit(Math.floor(sec / 86400), 'day');
}

/** "in 30s", or "now" once it's due. */
export function until(iso: string | null | undefined, now: number): string {
  const t = ms(iso);
  if (t === null) return '';
  return t <= now ? 'now' : 'in ' + span(t - now);
}

/** A clock time for a message: 21:44:10 today, or Nov 8, 21:44 before. */
export function clock(iso: string, now: number): string {
  const t = ms(iso);
  if (t === null) return '';
  const d = new Date(t);
  const today = new Date(now);
  if (d.toDateString() === today.toDateString()) {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
  }
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false });
}

// One shared ticker keeps every "12s ago" on the page current.
const TICK = 5000;
let nowValue = Date.now();
const subscribers = new Set<() => void>();
let timer: ReturnType<typeof setInterval> | null = null;

function subscribe(fn: () => void) {
  subscribers.add(fn);
  if (!timer) {
    timer = setInterval(() => {
      nowValue = Date.now();
      subscribers.forEach((s) => s());
    }, TICK);
  }
  return () => {
    subscribers.delete(fn);
    if (!subscribers.size && timer) {
      clearInterval(timer);
      timer = null;
    }
  };
}

/** The current time, updated every few seconds. */
export function useNow(): number {
  return useSyncExternalStore(subscribe, () => nowValue);
}
