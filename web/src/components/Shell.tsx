import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';
import { Button, Logo } from '../ds';
import { ME } from '../lib/agents';
import { connection, reachable, useConnection } from '../lib/connection';
import { href, type Route } from '../lib/router';
import { setTheme, useTheme, type ThemeChoice } from '../lib/theme';
import { ago, useNow } from '../lib/time';
import { AgentTile } from './AgentTile';
import { Signal } from './Signal';

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
        <Logo variant="mark" size={44} />
        <span aria-hidden="true" className="app-logo-word">birdie</span>
      </a>
      <nav className="app-nav" aria-label="Main">
        {NAV.map((n) => (
          <a key={n.page} href={n.href} aria-current={route.page === n.page ? 'page' : undefined}>
            {n.label}
          </a>
        ))}
      </nav>
      <div className="app-header-end">
        <Signal className="app-status" label={ok ? 'birdie is running on ' + conn.address : "birdie isn't reachable"} live={ok} />
        {route.page !== 'agents' && (
          <Button kind="primary" href={href.send}>
            Send a message
          </Button>
        )}
        <ThemeToggle />
        <YouMenu />
      </div>
    </header>
  );
}

const THEMES: { value: ThemeChoice; label: string; icon: ReactNode }[] = [
  {
    value: 'light',
    label: 'Light',
    icon: (
      <>
        <circle cx={12} cy={12} r={4} />
        <path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M5.3 5.3l1.6 1.6M17.1 17.1l1.6 1.6M5.3 18.7l1.6-1.6M17.1 6.9l1.6-1.6" />
      </>
    ),
  },
  {
    value: 'dark',
    label: 'Dark',
    icon: <path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5Z" />,
  },
  {
    value: 'system',
    label: 'System',
    icon: (
      <>
        <rect x={3} y={4.5} width={18} height={12} rx={2} />
        <path d="M8.5 20h7M12 16.5V20" />
      </>
    ),
  },
];

/** Light, dark, or whatever the system uses. */
function ThemeToggle() {
  const theme = useTheme();
  return (
    <div className="theme-toggle" role="radiogroup" aria-label="Theme">
      {THEMES.map((t) => (
        <button
          key={t.value}
          type="button"
          role="radio"
          aria-checked={theme === t.value}
          aria-label={t.label}
          title={t.label}
          onClick={() => setTheme(t.value)}
        >
          <svg viewBox="0 0 24 24" width={18} height={18} aria-hidden="true">
            {t.icon}
          </svg>
        </button>
      ))}
    </div>
  );
}

/** Your face, top right, with signing out behind it. */
function YouMenu() {
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', away);
    document.addEventListener('keydown', esc);
    return () => {
      document.removeEventListener('pointerdown', away);
      document.removeEventListener('keydown', esc);
    };
  }, [open]);
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
    <div className="you-menu" ref={box}>
      <button type="button" className="you-button" aria-label="You" aria-expanded={open} aria-haspopup="true" onClick={() => setOpen((v) => !v)}>
        <AgentTile name={ME} size={40} />
      </button>
      {open && (
        <div className="you-pop">
          <div className="you-pop-head">
            <AgentTile name={ME} size={44} />
            <div>
              <strong>You</strong>
              <span>Agents' answers to you show up in Messages.</span>
            </div>
          </div>
          <button type="button" className="you-pop-item" onClick={signOut}>
            Sign out
          </button>
        </div>
      )}
    </div>
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
  return (
    <footer className="app-footer">
      <span>{version ? 'birdie ' + version : 'birdie'}</span>
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
