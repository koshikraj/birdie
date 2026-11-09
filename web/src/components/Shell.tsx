import type { ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';
import { Button, Logo, StatusSignal } from '../ds';
import { connection, reachable, useConnection } from '../lib/connection';
import { href, type Route } from '../lib/router';
import { ago, useNow } from '../lib/time';

const NAV: { page: Route['page']; label: string; href: string }[] = [
  { page: 'dashboard', label: 'Dashboard', href: href.dashboard },
  { page: 'agents', label: 'Agents', href: href.agents },
  { page: 'messages', label: 'Messages', href: href.messages },
];

export function Header({ route }: { route: Route }) {
  const conn = useConnection();
  const ok = reachable(conn);
  return (
    <header className="app-header">
      <a href={href.dashboard} aria-label="birdie dashboard" className="app-logo">
        <Logo variant="lockup" size={30} />
      </a>
      <nav className="app-nav" aria-label="Main">
        {NAV.map((n) => (
          <a key={n.page} href={n.href} aria-current={route.page === n.page ? 'page' : undefined}>
            {n.label}
          </a>
        ))}
      </nav>
      <div className="grow" />
      <StatusSignal label={ok ? 'birdie is running on ' + conn.address : "birdie isn't reachable"} live={ok} />
      {route.page !== 'agents' && (
        <Button kind="primary" href={href.send}>
          Send a message
        </Button>
      )}
    </header>
  );
}

/** Says when birdie can't be reached, and that what's on screen is stale. */
export function Banner() {
  const conn = useConnection();
  const now = useNow();
  if (reachable(conn)) return null;
  return (
    <div className="banner" role="status">
      <span>
        <strong>birdie isn't reachable</strong> at <span className="mono">{conn.address}</span>. Start it with: <code>birdie service install</code>
      </span>
      <span className="muted">
        {conn.lastOk ? 'What you see is from ' + ago(new Date(conn.lastOk).toISOString(), now) + '. ' : ''}
        This page reconnects on its own.
      </span>
    </div>
  );
}

export function Footer({ version }: { version?: string }) {
  const client = useQueryClient();
  const signOut = async () => {
    try {
      await api.logout();
    } catch {
      // Signed out either way once the cookie is gone or unusable.
    }
    client.clear();
    connection.signedOut('You signed out.');
  };
  return (
    <footer className="app-footer">
      <span>{version ? 'birdie ' + version : 'birdie'}</span>
      <button type="button" className="link-button" onClick={signOut}>
        Sign out
      </button>
    </footer>
  );
}

export function CenterPage({ children }: { children: ReactNode }) {
  return (
    <div className="center-page">
      <main className="center-card">{children}</main>
    </div>
  );
}

export function SignIn({ note }: { note: string }) {
  return (
    <CenterPage>
      <Logo variant="lockup" size={40} />
      <h1>Open the app with birdie ui</h1>
      {note && <p>{note}</p>}
      <p>
        This page needs a sign-in code from birdie. In a terminal on this machine, run <code className="mono">birdie ui</code>. It opens the app here
        and signs you in.
      </p>
    </CenterPage>
  );
}
