import claudeIcon from '../assets/claude-code.svg';
import codexIcon from '../assets/codex.svg';
import grokBotIcon from '../assets/grok-bot.png';
import grokIcon from '../assets/grok.svg';
import hermesIcon from '../assets/hermes.png';
import type { Agent, Delivery, Overview, Pair, Session } from '../api/types';

export type BirdSpec = 'dot' | 'dash' | 'arch' | 'box';
export type AgentColor = 1 | 2 | 3 | 4;

/** How an agent looks: its icon tile, and the bird it turns into. */
export interface Identity {
  product: string;
  /** The tile's background, a CSS color. */
  tile: string;
  glyph: 'icon' | 'letter' | 'face';
  icon?: string;
  /** The icon fills its tile edge to edge. */
  full: boolean;
  letter: string;
  spec: BirdSpec;
  color: AgentColor;
  /** The icon's width in the live scene's 68px tile, and whether it's clipped to the tile. */
  sceneIcon: number;
  sceneClip: boolean;
}

const SPEC_ORDER: BirdSpec[] = ['dot', 'dash', 'arch', 'box'];

function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

/** The agent the web app and the CLI send as. That's you. */
export const ME = 'me';

export function identity(name: string): Identity {
  const base = { letter: (name[0] || '?').toUpperCase(), sceneClip: false };
  if (name === ME) {
    return { ...base, product: 'You', tile: 'var(--you-tile)', glyph: 'face', full: true, spec: 'dot', color: 1, sceneIcon: 68, sceneClip: true };
  }
  if (name === 'claude') {
    return { ...base, product: 'Claude Code', tile: '#D97757', glyph: 'icon', icon: claudeIcon, full: false, spec: 'dot', color: 1, sceneIcon: 50 };
  }
  if (name === 'codex') {
    return { ...base, product: 'Codex', tile: '#FFFFFF', glyph: 'icon', icon: codexIcon, full: false, spec: 'dash', color: 2, sceneIcon: 52 };
  }
  if (name === 'hermes' || name.startsWith('hermes-')) {
    return { ...base, product: 'Hermes Agent', tile: '#FFFFFF', glyph: 'icon', icon: hermesIcon, full: true, spec: 'box', color: 4, sceneIcon: 58 };
  }
  if (name === 'grok-bot') {
    return { ...base, product: 'Grok Bot', tile: '#000000', glyph: 'icon', icon: grokBotIcon, full: true, spec: 'arch', color: 3, sceneIcon: 68, sceneClip: true };
  }
  if (name === 'grok' || name === 'grok-tui') {
    return { ...base, product: 'Grok CLI', tile: '#000000', glyph: 'icon', icon: grokIcon, full: false, spec: 'arch', color: 3, sceneIcon: 44 };
  }
  const h = hash(name);
  const color = ((h % 4) + 1) as AgentColor;
  return {
    ...base,
    product: name,
    tile: 'var(--agent-' + color + ')',
    glyph: 'letter',
    full: false,
    spec: SPEC_ORDER[(h >>> 3) % 4],
    color,
    sceneIcon: 0,
  };
}

/** The agent part of a label such as "claude/birdie". */
export function agentOf(label: string): string {
  const i = label.indexOf('/');
  return i < 0 ? label : label.slice(0, i);
}

/** A label as you read it: anything of `me` is you. */
export function who(label: string): string {
  return agentOf(label) === ME ? 'you' : label;
}

/** How other agents address a session: agent/name. */
export function sessionAddress(s: Session): string {
  return s.agent + '/' + (s.name || s.id.slice(0, 8));
}

export function liveSessions(a: Agent): Session[] {
  return a.sessions.filter((s) => s.live);
}

/** The session a message to the bare agent name goes to, and why. */
export function defaultSession(a: Agent): { session: Session; why: string } | null {
  const live = liveSessions(a);
  const def = live.find((s) => s.default);
  if (def) return { session: def, why: live.length === 1 ? 'its only session' : 'its default session' };
  if (live.length === 1) return { session: live[0], why: 'its only session' };
  return null;
}

export function deliveryLabel(d: Delivery): string {
  if (d === 'pull') return 'pull · sessions';
  if (d === 'acp') return 'headless';
  return d;
}

/** Who each agent talked to today, from the overview's pairs. */
export function peersOf(pairs: Pair[]): Map<string, string[]> {
  const peers = new Map<string, string[]>();
  const add = (a: string, b: string) => {
    const list = peers.get(a) || [];
    if (!list.includes(b)) list.push(b);
    peers.set(a, list);
  };
  for (const p of pairs) {
    if (p.count <= 0) continue;
    add(p.a, p.b);
    add(p.b, p.a);
  }
  return peers;
}

/** Shortens a home folder to ~. */
export function homePath(p: string | undefined): string {
  if (!p) return '';
  return p.replace(/^\/(home|Users)\/[^/]+(?=\/|$)/, '~');
}

/**
 * The agents to show: everyone but `me`, the agent the web app and the CLI
 * send as. That is you, so it never appears as an agent of its own.
 */
export function agentsOf(o: Overview | undefined): Agent[] {
  return (o?.agents ?? []).filter((a) => a.name !== o?.me);
}

export function findAgent(o: Overview | undefined, name: string): Agent | undefined {
  return o?.agents.find((a) => a.name === name);
}
