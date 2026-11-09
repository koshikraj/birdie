import { connection } from '../lib/connection';
import type {
  ApiError,
  Overview,
  RecentMessages,
  SendInput,
  SendResult,
  Thread,
  ThreadList,
  ToolList,
} from './types';

export const API = '/ui/api';

/** A failed API call. status is 0 when birdie couldn't be reached at all. */
export class ApiFailure extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(API + path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    connection.apiUnreachable();
    throw new ApiFailure(0, "birdie isn't reachable");
  }

  // A proxy in front of a stopped birdie answers with one of these.
  if (res.status === 502 || res.status === 503 || res.status === 504) {
    connection.apiUnreachable();
    throw new ApiFailure(0, "birdie isn't reachable");
  }
  // birdie answers its own errors with JSON. A 5xx without it comes from
  // something in between, such as the dev server's proxy, while birdie is down.
  let errBody = '';
  if (res.status >= 500) {
    errBody = await res.text().catch(() => '');
    let fromBirdie = false;
    try {
      fromBirdie = typeof (JSON.parse(errBody) as Partial<ApiError>).error === 'string';
    } catch {
      fromBirdie = false;
    }
    if (!fromBirdie) {
      connection.apiUnreachable();
      throw new ApiFailure(0, "birdie isn't reachable");
    }
  }
  connection.apiReached();

  if (res.status === 401) {
    connection.signedOut();
    throw new ApiFailure(401, 'Sign in again with birdie ui');
  }
  if (!res.ok) {
    let message = res.statusText || 'HTTP ' + res.status;
    try {
      const data = JSON.parse(errBody || (await res.text())) as Partial<ApiError>;
      if (data && typeof data.error === 'string' && data.error) message = data.error;
    } catch {
      // Not JSON: keep the status text.
    }
    throw new ApiFailure(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

export const api = {
  overview: () => request<Overview>('GET', '/overview'),
  messages: (limit = 20) => request<RecentMessages>('GET', '/messages?limit=' + limit),
  threads: (limit = 30) => request<ThreadList>('GET', '/threads?limit=' + limit),
  thread: (id: string) => request<Thread>('GET', '/threads/' + encodeURIComponent(id)),
  send: (input: SendInput) => request<SendResult>('POST', '/messages', input),
  tools: () => request<ToolList>('GET', '/tools'),
  login: (code: string) => request<void>('POST', '/login', { code }),
  me: () => request<unknown>('GET', '/me'),
  logout: () => request<void>('POST', '/logout'),
};
