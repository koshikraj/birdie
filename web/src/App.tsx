import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ApiFailure, api } from './api/client';
import { startEvents } from './api/events';
import { useOverview } from './api/hooks';
import { Banner, CenterPage, Footer, Header, SignIn } from './components/Shell';
import { Logo } from './ds';
import { connection, useConnection } from './lib/connection';
import { useRoute } from './lib/router';
import { AgentsPage } from './pages/Agents';
import { DashboardPage } from './pages/Dashboard';
import { MessagesPage } from './pages/Messages';

/**
 * `birdie ui` opens /ui/#login=CODE. The code is swapped for a cookie, and
 * taken out of the address bar first so it never lingers in history.
 */
async function signInNow() {
  const m = window.location.hash.match(/(?:^#|[&/?])login=([^&]+)/);
  if (m) {
    let code = m[1];
    try {
      code = decodeURIComponent(code);
    } catch {
      // Use it as it is.
    }
    window.history.replaceState(null, '', window.location.pathname + window.location.search + '#/');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    try {
      await api.login(code);
    } catch (err) {
      if (err instanceof ApiFailure && err.status >= 400 && err.status < 500) {
        connection.signedOut('That sign-in link has expired or was already used.');
        return;
      }
    }
  }
  try {
    await api.me();
    connection.signedIn();
  } catch (err) {
    if (err instanceof ApiFailure && err.status === 401) return; // The client already showed sign-in.
    // birdie isn't reachable: show the app with its banner. A 401 later still shows sign-in.
    connection.signedIn();
  }
}

// One sign-in at a time, so a second call can't ask /me before the login lands.
let inflight: Promise<void> | null = null;
function signIn(): Promise<void> {
  if (!inflight) inflight = signInNow().finally(() => (inflight = null));
  return inflight;
}

export function App() {
  const conn = useConnection();

  useEffect(() => {
    signIn();
    const onHash = () => {
      if (/login=/.test(window.location.hash)) signIn();
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  if (conn.auth === 'checking') {
    return (
      <CenterPage>
        <Logo variant="lockup" size={40} />
        <p role="status">Connecting to birdie.</p>
      </CenterPage>
    );
  }
  if (conn.auth === 'signed-out') return <SignIn note={conn.authNote} />;
  return <SignedIn />;
}

function SignedIn() {
  const client = useQueryClient();
  const route = useRoute();
  const overview = useOverview();

  useEffect(() => startEvents(client), [client]);

  useEffect(() => {
    const names = { dashboard: 'birdie', agents: 'Agents · birdie', messages: 'Messages · birdie' };
    document.title = names[route.page];
  }, [route.page]);

  return (
    <div className="app">
      <a className="skip-link" href="#main" onClick={(e) => {
        e.preventDefault();
        document.getElementById('main')?.focus();
      }}>
        Skip to content
      </a>
      <Header route={route} />
      <Banner />
      <main id="main" tabIndex={-1} className="app-main">
        {route.page === 'dashboard' && <DashboardPage />}
        {route.page === 'agents' && <AgentsPage section={route.section} />}
        {route.page === 'messages' && <MessagesPage id={route.id} />}
      </main>
      <Footer version={overview.data?.version} />
    </div>
  );
}
