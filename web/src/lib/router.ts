import { useEffect, useState } from 'react';

export type Route =
  | { page: 'dashboard' }
  | { page: 'agents'; section?: 'send' | 'add'; to?: string }
  | { page: 'messages'; id?: string };

export function parseHash(hash: string): Route {
  const path = hash.replace(/^#/, '').split('?')[0];
  const parts = path.split('/').filter(Boolean).map((p) => {
    try {
      return decodeURIComponent(p);
    } catch {
      return p;
    }
  });
  if (parts[0] === 'agents') {
    if (parts[1] === 'to' && parts[2]) return { page: 'agents', to: parts[2] };
    const section = parts[1] === 'send' || parts[1] === 'add' ? parts[1] : undefined;
    return { page: 'agents', section };
  }
  if (parts[0] === 'messages') return { page: 'messages', id: parts[1] || undefined };
  return { page: 'dashboard' };
}

export const href = {
  dashboard: '#/',
  agents: '#/agents',
  send: '#/agents/send',
  add: '#/agents/add',
  /** The Agents page with that agent picked to message. */
  agent: (name: string) => '#/agents/to/' + encodeURIComponent(name),
  messages: '#/messages',
  thread: (id: string) => '#/messages/' + encodeURIComponent(id),
};

/** The current hash route, kept in step with the address bar. */
export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseHash(window.location.hash));
  useEffect(() => {
    const on = () => setRoute(parseHash(window.location.hash));
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return route;
}
