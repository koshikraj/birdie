import { useEffect, useRef } from 'react';
import type { QueryClient } from '@tanstack/react-query';
import { connection, subscribeConnection } from '../lib/connection';
import { API, ApiFailure, api } from './client';
import { keys } from './hooks';
import type { BusEvent } from './types';

type Listener = (e: BusEvent) => void;
const listeners = new Set<Listener>();

/** Calls fn for every event from the stream until the returned function runs. */
export function onBusEvent(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Subscribes a component to stream events, always calling its latest handler. */
export function useBusEvent(fn: Listener) {
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(() => onBusEvent((e) => ref.current(e)), []);
}

/**
 * Opens the event stream, hands each event to the listeners, and refetches the
 * data an event touches, debounced so a burst costs one round of requests.
 * Reconnects with a backoff whenever the stream closes.
 */
export function startEvents(client: QueryClient): () => void {
  let es: EventSource | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let backoff = 1000;
  let stopped = false;
  let wasDown = false;

  const pending = new Set<string>();
  let flushTimer: ReturnType<typeof setTimeout> | null = null;
  const refetch = (what: string[]) => {
    what.forEach((w) => pending.add(w));
    if (flushTimer) clearTimeout(flushTimer);
    flushTimer = setTimeout(() => {
      flushTimer = null;
      if (pending.has('overview')) client.invalidateQueries({ queryKey: keys.overview });
      if (pending.has('messages')) {
        client.invalidateQueries({ queryKey: keys.messages });
        client.invalidateQueries({ queryKey: keys.threads });
        client.invalidateQueries({ queryKey: ['thread'] });
      }
      pending.clear();
    }, 500);
  };

  const handle = (raw: MessageEvent<string>) => {
    let e: BusEvent;
    try {
      e = JSON.parse(raw.data) as BusEvent;
    } catch {
      return;
    }
    if (!e || (e.kind !== 'message' && e.kind !== 'session')) return;
    listeners.forEach((l) => {
      try {
        l(e);
      } catch (err) {
        console.error(err);
      }
    });
    refetch(e.kind === 'message' ? ['overview', 'messages'] : ['overview']);
  };

  const connect = () => {
    if (stopped) return;
    es = new EventSource(API + '/events', { withCredentials: true });
    es.addEventListener('open', () => {
      backoff = 1000;
      connection.streamOpen();
      // Catch up on anything that happened while the stream was down.
      if (wasDown) client.invalidateQueries();
      wasDown = false;
    });
    es.addEventListener('message', handle as EventListener);
    es.addEventListener('session', handle as EventListener);
    es.addEventListener('error', () => {
      wasDown = true;
      connection.streamDown();
      if (!es || es.readyState !== EventSource.CLOSED) return; // The browser retries by itself.
      es.close();
      es = null;
      // A closed stream can mean the cookie expired: /me says so, and a 401 shows sign-in.
      const again = () => {
        if (stopped) return;
        retryTimer = setTimeout(connect, backoff);
        backoff = Math.min(backoff * 2, 15000);
      };
      api.me().then(again, (err) => {
        if (!(err instanceof ApiFailure && err.status === 401)) again();
      });
    });
  };

  // When the API can't reach birdie, the stream is gone too, even if a proxy
  // holds it half open. Start over, and catch up once birdie answers again.
  let apiOk = connection.get().apiOk;
  const unwatch = subscribeConnection(() => {
    const ok = connection.get().apiOk;
    if (ok === apiOk) return;
    apiOk = ok;
    if (ok) {
      client.invalidateQueries();
      return;
    }
    wasDown = true;
    connection.streamDown();
    if (es) {
      es.close();
      es = null;
      if (retryTimer) clearTimeout(retryTimer);
      retryTimer = setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 15000);
    }
  });

  connect();
  return () => {
    stopped = true;
    unwatch();
    if (retryTimer) clearTimeout(retryTimer);
    if (flushTimer) clearTimeout(flushTimer);
    es?.close();
  };
}
