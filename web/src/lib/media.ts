import { useEffect, useState } from 'react';

/** Whether a media query matches, kept up to date. */
export function useMedia(query: string): boolean {
  const [matches, setMatches] = useState(() => typeof window !== 'undefined' && !!window.matchMedia && window.matchMedia(query).matches);
  useEffect(() => {
    if (!window.matchMedia) return;
    const mq = window.matchMedia(query);
    const on = () => setMatches(mq.matches);
    on();
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, [query]);
  return matches;
}

export const useReducedMotion = () => useMedia('(prefers-reduced-motion: reduce)');

/** Follows the system's light or dark setting by setting data-theme on the root. */
export function followColorScheme() {
  const root = document.documentElement;
  const mq = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
  const apply = () => root.setAttribute('data-theme', mq && mq.matches ? 'dark' : 'light');
  apply();
  mq?.addEventListener('change', apply);
}
