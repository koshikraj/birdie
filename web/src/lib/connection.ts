import { useSyncExternalStore } from 'react';

declare const __BIRDIE_DEV_ADDRESS__: string;

export type Auth = 'checking' | 'ok' | 'signed-out';

export interface Connection {
  auth: Auth;
  /** Why the sign-in screen shows, when there's more to say. */
  authNote: string;
  /** The last API call reached birdie. */
  apiOk: boolean;
  /** The event stream is open, or hasn't failed yet. */
  streamOk: boolean;
  /** When birdie was last reached. */
  lastOk: number | null;
  /** Where birdie listens, from the overview once known. */
  address: string;
}

function guessAddress(): string {
  if (typeof __BIRDIE_DEV_ADDRESS__ === 'string' && __BIRDIE_DEV_ADDRESS__) return __BIRDIE_DEV_ADDRESS__;
  return window.location.host || '127.0.0.1:7777';
}

let state: Connection = {
  auth: 'checking',
  authNote: '',
  apiOk: true,
  streamOk: true,
  lastOk: null,
  address: guessAddress(),
};
const listeners = new Set<() => void>();

function set(patch: Partial<Connection>) {
  const next = { ...state, ...patch };
  const changed = (Object.keys(patch) as (keyof Connection)[]).some((k) => next[k] !== state[k]);
  if (!changed) return;
  state = next;
  listeners.forEach((l) => l());
}

export const connection = {
  get: () => state,
  apiReached: () => set({ apiOk: true, lastOk: Date.now() }),
  apiUnreachable: () => set({ apiOk: false }),
  streamOpen: () => set({ streamOk: true, lastOk: Date.now() }),
  streamDown: () => set({ streamOk: false }),
  signedIn: () => set({ auth: 'ok', authNote: '' }),
  signedOut: (note = '') => set({ auth: 'signed-out', authNote: note }),
  setAddress: (address: string) => {
    if (address) set({ address });
  },
};

export function subscribeConnection(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}
const subscribe = subscribeConnection;

export function useConnection(): Connection {
  return useSyncExternalStore(subscribe, () => state);
}

/** birdie can be reached right now. */
export function reachable(c: Connection): boolean {
  return c.apiOk && c.streamOk;
}
