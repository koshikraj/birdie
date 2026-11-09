import { useSyncExternalStore } from 'react';

export type ThemeChoice = 'system' | 'light' | 'dark';

const KEY = 'birdie-theme';
const listeners = new Set<() => void>();

function read(): ThemeChoice {
  try {
    const v = window.localStorage.getItem(KEY);
    if (v === 'light' || v === 'dark' || v === 'system') return v;
  } catch {
    // Storage can be blocked. Follow the system then.
  }
  return 'system';
}

let choice: ThemeChoice = typeof window === 'undefined' ? 'system' : read();
const dark = typeof window !== 'undefined' && window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;

function apply() {
  const on = choice === 'dark' || (choice === 'system' && !!dark?.matches);
  document.documentElement.setAttribute('data-theme', on ? 'dark' : 'light');
}

/** Sets data-theme on the root from your choice, following the system when it's "system". */
export function startTheme() {
  apply();
  dark?.addEventListener('change', apply);
}

export function setTheme(next: ThemeChoice) {
  choice = next;
  try {
    if (next === 'system') window.localStorage.removeItem(KEY);
    else window.localStorage.setItem(KEY, next);
  } catch {
    // Applies for this visit only.
  }
  apply();
  listeners.forEach((l) => l());
}

export function useTheme(): ThemeChoice {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => choice,
  );
}
