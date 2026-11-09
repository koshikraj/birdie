import { QueryCache, QueryClient, useQuery } from '@tanstack/react-query';
import { useEffect } from 'react';
import { connection } from '../lib/connection';
import { ApiFailure, api } from './client';

export const queryClient = new QueryClient({
  queryCache: new QueryCache(),
  defaultOptions: {
    queries: {
      // Keep showing what we had while birdie is away; the banner says it's stale.
      retry: (count, err) => !(err instanceof ApiFailure && err.status >= 400 && err.status < 500) && count < 2,
      retryDelay: (n) => Math.min(1000 * 2 ** n, 8000),
      staleTime: 2000,
      refetchOnWindowFocus: true,
    },
  },
});

export const keys = {
  overview: ['overview'] as const,
  messages: ['messages'] as const,
  threads: ['threads'] as const,
  thread: (id: string) => ['thread', id] as const,
  tools: ['tools'] as const,
};

export function useOverview() {
  const q = useQuery({ queryKey: keys.overview, queryFn: api.overview, refetchInterval: 15000 });
  const address = q.data?.address;
  useEffect(() => {
    if (address) connection.setAddress(address);
  }, [address]);
  return q;
}

export function useRecentMessages() {
  return useQuery({ queryKey: keys.messages, queryFn: () => api.messages(20), refetchInterval: 30000 });
}

export function useThreads() {
  return useQuery({ queryKey: keys.threads, queryFn: () => api.threads(30), refetchInterval: 30000 });
}

export function useThread(id: string | undefined) {
  return useQuery({
    queryKey: keys.thread(id || ''),
    queryFn: () => api.thread(id || ''),
    enabled: !!id,
    retry: (count, err) => !(err instanceof ApiFailure && err.status === 404) && count < 2,
  });
}

export function useTools(enabled: boolean) {
  return useQuery({ queryKey: keys.tools, queryFn: api.tools, enabled, staleTime: 60000 });
}
