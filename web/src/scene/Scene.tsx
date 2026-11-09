import { useEffect, useId, useMemo, useRef, useState } from 'react';
import type { Agent, Overview, Tracked } from '../api/types';
import { useBusEvent } from '../api/events';
import { InstallCommand, Letter } from '../ds';
import { Signal } from '../components/Signal';
import { AgentTile } from '../components/AgentTile';
import { agentsOf, identity, liveSessions, peersOf, type Identity } from '../lib/agents';
import { href } from '../lib/router';
import { useMedia, useReducedMotion } from '../lib/media';
import { ms, useNow } from '../lib/time';
import { SPECS, T, at, back, bump, clamp, inOut, lerp, lut, outCubic, part, seg, type Lut, type Pt } from './geometry';
import { Player, type Flight, type SceneContext } from './player';
import './scene.css';

const MAX_PERCHES = 8;
const ASLEEP_AFTER = 5 * 60 * 1000;
const W = 1000;

interface Placed {
  name: string;
  agent: Agent;
  id: Identity;
  x: number;
  y: number;
  face: 1 | -1;
  left: boolean;
  route: [Pt, Pt, Pt, Pt];
  path: string;
  lut: Lut;
  port: Pt;
}

interface Layout {
  placed: Placed[];
  height: number;
  hubY: number;
}

/** Perches in two columns, left and right of the hub, each with a route to its own port on the hub. */
function layoutScene(slots: string[], agents: Map<string, Agent>): Layout {
  const rows = Math.max(1, Math.ceil(slots.length / 2));
  const pitch = rows <= 2 ? 260 : 232;
  const firstY = 176;
  const lastY = firstY + (rows - 1) * pitch;
  const hubY = (firstY + lastY) / 2 - 26;
  const sides: [string[], string[]] = [[], []];
  slots.forEach((name, i) => sides[i % 2].push(name));
  const placed: Placed[] = [];
  slots.forEach((name, i) => {
    const agent = agents.get(name);
    if (!agent) return;
    const left = i % 2 === 0;
    const row = Math.floor(i / 2);
    const x = left ? 150 : 850;
    const y = firstY + row * pitch;
    const side = sides[i % 2];
    const k = side.indexOf(name);
    const py = hubY + (k - (side.length - 1) / 2) * 28;
    const wy = y + 2.5;
    const route: [Pt, Pt, Pt, Pt] = left
      ? [[214, wy], [330, wy], [320, py], [425, py]]
      : [[786, wy], [670, wy], [680, py], [575, py]];
    const path = 'M' + route[0].join(' ') + ' C' + route[1].join(' ') + ' ' + route[2].join(' ') + ' ' + route[3].join(' ');
    placed.push({ name, agent, id: identity(name), x, y, face: left ? 1 : -1, left, route, path, lut: lut(route), port: route[3] });
  });
  return { placed, height: lastY + 84, hubY };
}

/** Keeps each agent on the perch it already has, filling freed perches with newly active agents. */
function stableSlots(prev: string[], desired: string[]): string[] {
  const want = new Set(desired);
  const next: (string | null)[] = new Array(desired.length).fill(null);
  prev.forEach((name, i) => {
    if (i < next.length && want.has(name)) next[i] = name;
  });
  const missing = desired.filter((d) => !next.includes(d));
  return next.map((n) => n ?? (missing.shift() as string));
}

const hidden = { x: 0, y: 0, r: 0, o: 0 };
const f1 = (n: number) => n.toFixed(1);
const f2 = (n: number) => n.toFixed(2);

/** The hub's logo pops into the small icon's dot and upright dash as a letter passes through. */
function logoFrame(u: number) {
  const POP = 0.6;
  const amount = (out: number, backAt: number) =>
    u < backAt ? back(seg(u, out, out + POP), 1.7) : 1 - back(seg(u, backAt, backAt + POP), 1.7);
  const d = amount(0, 0.75);
  const l = amount(0.12, 0.87);
  const sqD = Math.sin(Math.PI * clamp(d));
  const sqL = Math.sin(Math.PI * clamp(l));
  const r = Math.max(1, lerp(24, 27.5, d)) * (1 - sqD * 0.28);
  const w0 = lerp(30, 32.5, l);
  const h0 = lerp(64, 75, l);
  const w = Math.max(4, w0 * (1 + sqL * 0.3));
  const h = Math.max(4, h0 * (1 - sqL * 0.34));
  const bottom = lerp(86, 97.5, l);
  const cx = lerp(89, 91.25, l);
  const fadeD = 1 - seg(clamp(d), 0, 0.45);
  const fadeL = 1 - seg(clamp(l), 0, 0.45);
  const gone = clamp(Math.max(d, l));
  const ww = 112 * (1 - seg(gone, 0.1, 0.8));
  const ls = 1 - seg(gone, 0, 0.5);
  const burst = (times: number[], bx: number, by: number) => {
    const out = Array.from({ length: 6 }, () => hidden);
    times.forEach((b) => {
      const q = seg(u, b, b + 0.55);
      if (q <= 0 || q >= 1) return;
      for (let k = 0; k < 6; k++) {
        const ang = (k / 6) * Math.PI * 2 + 0.4;
        const dd = outCubic(q) * 38;
        out[k] = { x: bx + Math.cos(ang) * dd, y: by + Math.sin(ang) * dd + q * q * 10, r: 5.5 * (1 - q) + 0.4, o: 1 - q };
      }
    });
    return out;
  };
  return {
    wireX: 60 - ww / 2, wireW: Math.max(0, ww),
    dotCx: lerp(38, 45, d), dotCy: lerp(62, 70, d) - sqD * 5, dotR: r,
    dotTail: part(16, 60, fadeD), dotBeak: part(59, 60, fadeD), dotEye: part(46, 54, fadeD),
    dashX: cx - w / 2, dashY: bottom - h, dashW: w, dashH: h, dashRx: Math.min(w, h) / 2,
    dashTail: part(100, 68, fadeL), dashBeak: part(75, 40, fadeL), dashEye: part(84, 38, fadeL),
    letter: ls <= 0.01 ? 'scale(0)' : 'translate(57 20) scale(' + ls.toFixed(3) + ') translate(-57 ' + (-20 - (1 - ls) * 8).toFixed(2) + ')',
    dots: burst([0.05, 0.8], 42, 66),
    dashes: burst([0.17, 0.92], 90, 60),
  };
}

function short(name: string, n = 16) {
  return name.length > n ? name.slice(0, n - 1) + '…' : name;
}

function clip(text: string, n = 140) {
  const t = text.replace(/\s+/g, ' ').trim();
  return t.length > n ? t.slice(0, n - 1) + '…' : t;
}

interface Props {
  overview?: Overview;
  recent?: Tracked[];
}

/** The live scene: each message flies from its sender's perch through birdie to its recipient. */
export function Scene({ overview, recent }: Props) {
  const now = useNow();
  const reduce = useReducedMotion();
  const compact = useMedia('(max-width: 640px)');
  const clipId = 'scene-tile-' + useId().replace(/[^a-zA-Z0-9]/g, '');

  const playerRef = useRef<Player | null>(null);
  if (!playerRef.current) playerRef.current = new Player();
  const player = playerRef.current;

  const [, setFrame] = useState(0);
  const repaint = () => setFrame((n) => (n + 1) % 1e9);
  const [paused, setPaused] = useState(false);
  const [eventTick, setEventTick] = useState(0);
  const activity = useRef(new Map<string, number>());
  const sawMessage = useRef(false);

  const agents = useMemo(() => agentsOf(overview), [overview]);
  const byName = useMemo(() => new Map(agents.map((a) => [a.name, a])), [agents]);

  // When each agent last sent or got a message.
  const lastActive = useMemo(() => {
    const m = new Map<string, number>();
    const note = (n: string, t: number | null) => {
      if (t !== null && t > (m.get(n) ?? 0)) m.set(n, t);
    };
    for (const a of agents) note(a.name, ms(a.health.delivered));
    for (const r of recent ?? []) {
      const t = Math.max(ms(r.created_at) ?? 0, ms(r.delivered_at) ?? 0);
      note(r.from, t);
      note(r.to, t);
    }
    for (const [n, t] of activity.current) note(n, t);
    return m;
    // eventTick brings in activity from the stream.
  }, [agents, recent, eventTick]);

  // Most recently active first; the rest are reachable from "+N more".
  const desired = useMemo(() => {
    const score = (a: Agent) => lastActive.get(a.name) ?? 0;
    const seen = (a: Agent) => Math.max(0, ...a.sessions.map((s) => ms(s.active_at) ?? 0));
    return [...agents]
      .sort((a, b) => score(b) - score(a) || seen(b) - seen(a) || a.name.localeCompare(b.name))
      .slice(0, MAX_PERCHES)
      .map((a) => a.name);
  }, [agents, lastActive]);

  const slotsRef = useRef<string[]>([]);
  const slotKey = desired.join('|');
  const slots = useMemo(() => {
    const next = stableSlots(slotsRef.current, desired);
    slotsRef.current = next;
    return next;
  }, [slotKey]);

  const layout = useMemo(() => layoutScene(slots, byName), [slots, byName]);

  const noMessages = !!overview && overview.today.sent === 0 && !(recent && recent.length) && !sawMessage.current;

  const sleeping = useMemo(() => {
    const s = new Set<string>();
    for (const p of layout.placed) {
      const a = p.agent;
      if (a.health.error) continue; // Greyed out instead.
      // Busy, or stopped until you answer it: either way it isn't idle.
      if (a.sessions.some((x) => x.live && (x.state === 'busy' || x.state === 'waiting'))) continue;
      if (noMessages || now - (lastActive.get(a.name) ?? 0) > ASLEEP_AFTER) s.add(a.name);
    }
    return s;
  }, [layout, lastActive, now, noMessages]);

  const ctxRef = useRef<SceneContext>({ placed: new Set(), sleeping: new Set() });
  ctxRef.current = { placed: new Set(layout.placed.map((p) => p.name)), sleeping };
  player.paused = paused;

  const sleepKey = [...sleeping].sort().join('|');
  useEffect(() => {
    player.setReduce(reduce);
    player.step(0, ctxRef.current);
    repaint();
  }, [reduce, player]);
  useEffect(() => {
    player.step(0, ctxRef.current);
    repaint();
  }, [sleepKey, slotKey, player]);

  // A flight wakes its two agents when it starts playing, so a sleeping
  // recipient dozes until its letter heads its way.
  player.onStart = (f) => {
    const t = Date.now();
    activity.current.set(f.from, t);
    activity.current.set(f.to, t);
    setEventTick((n) => n + 1);
  };

  useBusEvent((e) => {
    if (e.kind !== 'message') return;
    const t = Date.now();
    sawMessage.current = true;
    // Agents without a perch get one before their message plays.
    for (const n of [e.from, e.to]) if (e.type !== 'queued' || !ctxRef.current.placed.has(n)) activity.current.set(n, t);
    if (e.type === 'queued') {
      player.push({
        id: e.id, from: e.from, to: e.to, fromLabel: e.from_label || e.from, toLabel: e.to_label || e.to,
        text: e.text, tone: e.in_reply_to ? 'reply' : 'request', landed: false, failed: false,
      });
    } else if (e.type === 'claimed' || e.type === 'delivered' || e.type === 'replied') {
      player.update(e.id, 'landed');
    } else if (e.type === 'retrying' || e.type === 'failed') {
      player.update(e.id, 'failed');
    }
    setEventTick((n) => n + 1);
    if (player.reduce) {
      player.step(0, ctxRef.current);
      repaint();
    }
  });

  // One frame loop. It only repaints while something moves, and the z's of a
  // sleeping bird drift at a gentler frame rate.
  useEffect(() => {
    let raf = 0;
    let lastStep = 0;
    const loop = (ts: number) => {
      raf = requestAnimationFrame(loop);
      const moving = player.active();
      let dozing = false;
      if (!moving && !player.reduce && !player.paused) {
        for (const a of player.anims.values()) if (a.shut) dozing = true;
      }
      if (!moving && !dozing) {
        lastStep = 0;
        return;
      }
      if (!moving && lastStep && ts - lastStep < 50) return;
      const dt = lastStep ? Math.min(ts - lastStep, 100) / 1000 : 0;
      lastStep = ts;
      player.step(dt, ctxRef.current);
      repaint();
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [player]);

  const peers = useMemo(() => peersOf(overview?.pairs ?? []), [overview]);
  const extra = agents.length - layout.placed.length;

  if (!overview) {
    return (
      <section className="scene" aria-labelledby="live-title">
        <div className="scene-head">
          <h2 id="live-title" className="title">Live</h2>
        </div>
        <p className="scene-empty-text"><Signal label="Connecting to birdie" /></p>
      </section>
    );
  }

  if (!agents.length) {
    return (
      <section className="scene" aria-labelledby="live-title">
        <div className="scene-head">
          <h2 id="live-title" className="title">Live</h2>
        </div>
        <div className="scene-empty">
          <p className="scene-empty-title">No agents yet</p>
          <p className="scene-empty-text">Connect the agents on this machine, and their messages show up here as they move. Run this in a terminal:</p>
          <InstallCommand command="birdie onboard" />
        </div>
      </section>
    );
  }

  // Everything below reads the player's state for this frame.
  const cur = player.current;
  const m: Flight | null = cur?.f ?? null;
  const t = cur ? cur.t : -1;
  const shown = m ?? player.last;
  const placed = layout.placed;
  const pos = new Map(placed.map((p) => [p.name, p]));
  const fold = (shown?.tone ?? 'request') === 'request' ? 'var(--letter-fold-request)' : 'var(--letter-fold-reply)';
  const clockNow = player.clock;
  const moving = !!m && !paused && !reduce;

  const fadeOut = 1 - seg(t, T.FADE[0], T.FADE[1]);
  const routes = placed.map((p) => {
    const len = p.lut.len;
    let off = len;
    let on = false;
    if (m && p.name === m.from && t >= T.L1[0]) {
      on = true;
      off = len * (1 - inOut(seg(t, T.L1[0], T.L1[1])));
    }
    if (m && p.name === m.to && t >= T.L2[0]) {
      on = true;
      off = -len * (1 - inOut(seg(t, T.L2[0], T.L2[1])));
    }
    return { p, dash: f1(len) + ' ' + f1(len), off: f1(off), o: on ? fadeOut : 0 };
  });

  // The letter, tilted toward where it's going, with a short trail.
  let letter: { g: string; trail: { x: number; y: number; o: number }[] } | null = null;
  const fly = (p: Placed, e: number, outward: boolean) => {
    const dir = (outward ? p.left : !p.left) ? 1 : -1;
    const at0 = at(p.lut, outward ? e : 1 - e);
    const trail = [0, 1, 2].map((k) => {
      const e2 = Math.max(0, e - (k + 1) * 0.05);
      const p2 = at(p.lut, outward ? e2 : 1 - e2);
      return { x: p2[0], y: p2[1] - 22, o: e2 > 0 ? 0.5 - k * 0.14 : 0 };
    });
    return {
      g: 'translate(' + f1(at0[0]) + ' ' + f1(at0[1] - 22) + ') rotate(' + f1(Math.cos(Math.PI * e) * -14 * dir) + ') scale(1.15)',
      trail,
    };
  };
  if (m) {
    const from = pos.get(m.from);
    const to = pos.get(m.to);
    if (from && t >= T.L1[0] && t < T.L1[1]) letter = fly(from, inOut(seg(t, T.L1[0], T.L1[1])), true);
    if (to && t >= T.L2[0] && t < T.L2[1]) letter = fly(to, inOut(seg(t, T.L2[0], T.L2[1])), false);
  }

  const lg = logoFrame(cur ? cur.elapsed - T.HUB : -10);

  const birds = placed.map((pl, i) => {
    const a = player.anims.get(pl.name);
    const mv = a ? player.morphValue(a.morph) : { p: 0, color: 0, q: 1 };
    const p = mv.p;
    const mix = clamp(mv.color);
    const sp = SPECS[pl.id.spec];
    const w = Math.max(10, lerp(68, sp.w, p));
    const h = Math.max(10, lerp(68, sp.h, p));
    const rx = Math.min(Math.max(lerp(17, sp.rx, p), 4), w / 2, h / 2);
    const birdFill = 'var(--agent-' + pl.id.color + ')';
    const fill = mix >= 0.995 ? birdFill : mix <= 0.005 ? pl.id.tile : 'color-mix(in oklab, ' + birdFill + ' ' + (mix * 100).toFixed(1) + '%, ' + pl.id.tile + ')';
    const parts = clamp((p - 0.45) / 0.55);
    const eyes = clamp((p - 0.6) / 0.4);
    const shut = !!a?.shut;
    const involved = !!m && (m.from === pl.name || m.to === pl.name);
    const morph = a?.morph;
    let y = pl.y;
    if (morph && morph.to === 1 && morph.burst) y -= bump(clockNow, morph.start + 0.05, 0.6) * 22;
    if (a) y -= bump(clockNow, a.wakeAt, 0.5) * 26;
    if (m && m.to === pl.name && !m.failed) y -= bump(t, T.ARRIVE, 0.42) * 30;
    const lean = m && m.from === pl.name ? bump(t, 0.3, 0.5) * 16 * pl.face : 0;
    const pop = morph && morph.to === 1 && morph.burst && morph.dur ? 1 + Math.sin(Math.PI * seg(clockNow, morph.start, morph.start + morph.dur)) * 0.08 : 1;
    const problem = !!pl.agent.health.error;
    const grey = problem && !involved;

    const zs = [0, 1, 2].map((k) => {
      if (!shut || p <= 0.8) return { x: 0, y: 0, o: 0 };
      const q = reduce ? (k + 0.5) / 3 : (clockNow * 0.55 + k / 3) % 1;
      return { x: pl.x + 30 * pl.face + (k - 1) * 3 - q * 22 * pl.face, y: pl.y - 84 - q * 44, o: Math.sin(Math.PI * q) };
    });

    const bits = Array.from({ length: 6 }, () => hidden);
    if (morph && morph.burst && morph.dur) {
      const b = morph.start + morph.dur * 0.45;
      const q = seg(clockNow, b, b + 0.6);
      if (q > 0 && q < 1) {
        for (let k = 0; k < 6; k++) {
          const ang = (k / 6) * Math.PI * 2 + i;
          const d = outCubic(q) * 74;
          bits[k] = { x: pl.x + Math.cos(ang) * d, y: pl.y - 46 + Math.sin(ang) * d + q * q * 26, r: 6 * (1 - q) + 0.5, o: 1 - q };
        }
      }
    }

    const wingExtra = sp.wing[2] ? ' rotate(' + sp.wing[2] + ' ' + sp.wing[0] + ' ' + sp.wing[1] + ')' : '';
    const live = liveSessions(pl.agent);
    const n = (peers.get(pl.name) || []).length;
    let sessions: string;
    if (problem) sessions = pl.agent.health.retry ? 'retrying' : 'not delivered';
    else if (live.some((s) => s.state === 'waiting')) sessions = 'needs you';
    else if (pl.agent.delivery === 'pull') sessions = live.length ? live.length + (live.length === 1 ? ' session' : ' sessions') : 'no sessions';
    else sessions = pl.agent.delivery === 'acp' ? 'headless' : pl.agent.delivery;
    const detail = n + (n === 1 ? ' peer' : ' peers') + ' today · ' + sessions;
    let role = '';
    if (m && m.from === pl.name && m.to !== pl.name) role = 'sending';
    else if (m && m.to === pl.name) role = t >= T.ARRIVE ? (m.failed ? 'not yet' : 'got it') : t >= T.L2[0] ? 'receiving' : m.from === pl.name ? 'sending' : '';
    else if (shut) role = 'asleep';
    else if (problem) role = 'not reachable';

    return {
      pl, sp, p, w, h, rx, fill, parts, eyes, shut, y, lean, pop, grey, zs, bits, wingExtra, detail, role, problem,
      glyph: 1 - seg(mix, 0.05, 0.5),
    };
  });

  const routeText = shown ? shown.fromLabel + ' → ' + shown.toLabel : '';
  let caption: string;
  if (m) caption = t < T.ARRIVE ? clip(m.text) : (m.failed ? m.toLabel + " hasn't got it yet: " : m.toLabel + ' got it: ') + clip(m.text);
  else if (noMessages) caption = 'No messages yet';
  else caption = 'Waiting for the next message';

  const asleepNames = birds.filter((b) => b.shut).map((b) => b.pl.name);
  const problemNames = birds.filter((b) => b.problem).map((b) => b.pl.name);
  const sceneLabel =
    placed.length + (placed.length === 1 ? ' agent on a perch' : ' agents on perches') + ', joined to birdie.' +
    (m ? ' Now: ' + routeText + ', ' + clip(m.text) + '.' : noMessages ? ' No messages yet.' : '') +
    placed.map((p) => {
      const list = peers.get(p.name) || [];
      return list.length ? ' ' + p.name + ' talks to ' + list.join(', ') + '.' : '';
    }).join('') +
    asleepNames.map((n) => ' ' + n + ' is asleep.').join('') +
    problemNames.map((n) => ' ' + n + ' is not reachable.').join('');

  const head = (
    <div className="scene-head">
      <h2 id="live-title" className="title">Live</h2>
      <div className="scene-caption">
        <Signal label={caption} live={moving} />
      </div>
      {!reduce && (
        <button className="scene-pause" type="button" aria-pressed={paused} onClick={() => setPaused((v) => !v)}>
          {paused ? 'Play' : 'Pause'}
        </button>
      )}
    </div>
  );

  const more = extra > 0 && (
    <a className="scene-more" href={href.agents}>
      +{extra} more {extra === 1 ? 'agent' : 'agents'}
    </a>
  );

  const calm = noMessages && (
    <p className="scene-calm">
      Everyone is asleep until the first message. <a href={href.send}>Send one from Agents</a>
    </p>
  );

  if (compact) {
    return (
      <section className="scene" aria-labelledby="live-title">
        {head}
        {shown && (
          <p className="scene-compact-now">
            <Letter tone={shown.tone} size={22} />
            <span>{routeText}</span>
          </p>
        )}
        <ul className="scene-compact">
          {birds.map((b) => (
            <li key={b.pl.name} className={b.grey ? 'is-grey' : undefined}>
              <AgentTile name={b.pl.name} size={34} />
              <span className="scene-compact-name">{b.pl.name}</span>
              <span className="scene-compact-detail">{b.detail}</span>
              {b.role && <span className="scene-compact-role">{b.role}</span>}
            </li>
          ))}
        </ul>
        {calm}
        {more}
      </section>
    );
  }

  return (
    <section className="scene" aria-labelledby="live-title">
      {head}
      <div className="scene-stage">
        {shown && (
          <div className="scene-route-chip">
            <Letter tone={shown.tone} size={22} />
            {routeText}
          </div>
        )}
        <svg role="img" aria-label={sceneLabel} viewBox={'0 0 ' + W + ' ' + layout.height} className="scene-svg">
          <defs>
            <clipPath id={clipId}>
              <rect x={-34} y={-68} width={68} height={68} rx={17} />
            </clipPath>
          </defs>

          {placed.map((p) => (
            <path key={'base-' + p.name} d={p.path} fill="none" strokeLinecap="round" className="scene-route-base" />
          ))}
          {routes.map((r) => (
            <g key={'lit-' + r.p.name} opacity={f2(r.o)}>
              <path d={r.p.path} fill="none" strokeLinecap="round" strokeDasharray={r.dash} strokeDashoffset={r.off} className="scene-route-outer" />
              <path d={r.p.path} fill="none" strokeLinecap="round" strokeDasharray={r.dash} strokeDashoffset={r.off} className="scene-route-core" style={{ stroke: fold }} />
            </g>
          ))}
          {placed.map((p) => (
            <circle key={'port-' + p.name} cx={p.port[0]} cy={p.port[1]} r={5} className="scene-ink" />
          ))}
          {placed.map((p) => (
            <rect key={'perch-' + p.name} x={p.x - 64} y={p.y} width={128} height={5} rx={2.5} className="scene-ink" />
          ))}

          <rect x={425} y={layout.hubY - 75} width={150} height={150} rx={17} className="scene-hub" />
          <g transform={'translate(440 ' + (layout.hubY - 60) + ')'}>
            <rect x={f2(lg.wireX)} y={86} width={f2(lg.wireW)} height={6} rx={3} className="scene-ink" />
            <path d="M18 50 L2 46 L16 68 Z" transform={lg.dotTail} style={{ fill: 'var(--brand-tomato)' }} />
            <circle cx={f2(lg.dotCx)} cy={f2(lg.dotCy)} r={f2(lg.dotR)} style={{ fill: 'var(--brand-tomato)' }} />
            <path d="M59 54 L72 60 L59 66 Z" transform={lg.dotBeak} className="scene-ink" />
            <g transform={lg.dotEye}>
              <circle cx={46} cy={54} r={6.5} fill="#FFFFFF" />
              <circle cx={48} cy={54} r={3.2} fill="#0A2E2B" />
            </g>
            <path d="M100 58 L116 74 L100 78 Z" transform={lg.dashTail} style={{ fill: 'var(--agent-2)' }} />
            <rect x={f2(lg.dashX)} y={f2(lg.dashY)} width={f2(lg.dashW)} height={f2(lg.dashH)} rx={f2(lg.dashRx)} style={{ fill: 'var(--agent-2)' }} />
            <path d="M75 34 L63 40 L75 46 Z" transform={lg.dashBeak} className="scene-ink" />
            <g transform={lg.dashEye}>
              <circle cx={84} cy={38} r={6.5} fill="#FFFFFF" />
              <circle cx={82} cy={38} r={3.2} fill="#0A2E2B" />
            </g>
            <g transform={lg.letter}>
              <g transform="rotate(-8 57 20)">
                <rect x={46} y={12} width={22} height={16} rx={2} style={{ fill: 'var(--mark-letter)' }} />
                <path d="M46 13.5 L57 21 L68 13.5 L68 16 L57 23.5 L46 16 Z" style={{ fill: 'var(--mark-fold)' }} />
              </g>
            </g>
            {lg.dots.map((d, k) => (
              <circle key={'d' + k} cx={f1(d.x)} cy={f1(d.y)} r={f2(d.r)} opacity={f2(d.o)} style={{ fill: 'var(--brand-tomato)' }} />
            ))}
            {lg.dashes.map((d, k) => (
              <circle key={'s' + k} cx={f1(d.x)} cy={f1(d.y)} r={f2(d.r)} opacity={f2(d.o)} style={{ fill: 'var(--agent-2)' }} />
            ))}
          </g>
          <text x={500} y={layout.hubY + 102} textAnchor="middle" className="scene-hub-name">birdie</text>

          {birds.map((b) => {
            const sp = b.sp;
            const { pl } = b;
            const id = pl.id;
            return (
              <g
                key={'bird-' + pl.name}
                transform={'translate(' + pl.x + ' ' + f2(b.y) + ') rotate(' + f2(b.lean) + ') scale(' + (1.1 * b.pop).toFixed(3) + ')'}
                className={b.grey ? 'scene-grey' : undefined}
              >
                <g transform={'scale(' + pl.face + ' 1)'}>
                  <polygon points={sp.tailPts} transform={part(sp.tail[0], sp.tail[1], b.parts)} style={{ fill: 'var(--agent-' + id.color + ')' }} />
                  <rect x={f2(-b.w / 2)} y={f2(-b.h)} width={f2(b.w)} height={f2(b.h)} rx={f2(b.rx)} style={{ fill: b.fill }} />
                  <ellipse
                    cx={sp.wing[0]} cy={sp.wing[1]} rx={sp.wingR[0]} ry={sp.wingR[1]}
                    transform={part(sp.wing[0], sp.wing[1], b.parts, b.wingExtra)}
                    style={{ fill: 'var(--agent-' + id.color + '-wing)' }}
                  />
                  <polygon points={sp.beakPts} transform={part(sp.beak[0], sp.beak[1] + 8, b.parts)} className="scene-ink" />
                  <circle cx={sp.eye[0]} cy={sp.eye[1]} r={9} transform={part(sp.eye[0], sp.eye[1], b.shut ? 0 : b.eyes)} style={{ fill: 'var(--bird-eye)' }} />
                  <circle cx={sp.eye[0] + 3} cy={sp.eye[1]} r={4.5} transform={part(sp.eye[0] + 3, sp.eye[1], b.shut ? 0 : b.eyes)} style={{ fill: 'var(--bird-pupil)' }} />
                  <rect x={sp.eye[0] - 8} y={sp.eye[1] - 2} width={16} height={4} rx={2} transform={part(sp.eye[0], sp.eye[1], b.shut ? b.eyes : 0)} style={{ fill: 'var(--bird-eye)' }} />
                </g>
                {b.glyph > 0.001 && <Glyph id={id} clipId={clipId} opacity={b.glyph} />}
              </g>
            );
          })}

          {birds.map((b) =>
            b.bits.map((d, k) => (
              <circle key={'bit-' + b.pl.name + k} cx={f1(d.x)} cy={f1(d.y)} r={f2(d.r)} opacity={f2(d.o)} style={{ fill: 'var(--agent-' + b.pl.id.color + ')' }} />
            )),
          )}
          {birds.map((b) =>
            b.zs.map((z, k) => (
              <text key={'z-' + b.pl.name + k} x={f1(z.x)} y={f1(z.y)} opacity={f2(z.o)} className="scene-z">z</text>
            )),
          )}

          {birds.map((b) => (
            <g key={'label-' + b.pl.name}>
              <text x={b.pl.x} y={b.pl.y + 32} textAnchor="middle" className="scene-name">{short(b.pl.name)}</text>
              <text x={b.pl.x} y={b.pl.y + 52} textAnchor="middle" className={b.problem ? 'scene-detail scene-detail-problem' : 'scene-detail'}>{b.detail}</text>
              <text x={b.pl.x} y={b.pl.y + 74} textAnchor="middle" className="scene-role">{b.role}</text>
            </g>
          ))}

          {letter && (
            <>
              {letter.trail.map((d, k) => (
                <circle key={'trail' + k} cx={f1(d.x)} cy={f1(d.y)} r={3.5} opacity={f2(d.o)} className="scene-ink" />
              ))}
              <g transform={letter.g}>
                <rect x={-17} y={-12} width={34} height={24} rx={3} style={{ fill: 'var(--letter-edge)' }} />
                <rect x={-15} y={-10} width={30} height={20} rx={1.5} style={{ fill: 'var(--letter)' }} />
                <path d="M-15 -9 L0 2 L15 -9 L15 -5 L0 6 L-15 -5 Z" style={{ fill: fold }} />
              </g>
            </>
          )}
        </svg>
      </div>
      {calm}
      <div className="scene-legend">
        <span><Letter tone="request" size={22} />Request</span>
        <span><Letter tone="reply" size={22} />Answer</span>
        <span>The lit route runs from the sender, through birdie, to the recipient.</span>
        {more}
      </div>
    </section>
  );
}

/** The agent's product icon on its tile, fading as it turns into its bird. */
function Glyph({ id, clipId, opacity }: { id: Identity; clipId: string; opacity: number }) {
  const o = opacity.toFixed(3);
  if (id.glyph === 'icon' && id.icon) {
    const s = id.sceneIcon;
    return (
      <image
        href={id.icon}
        x={-s / 2}
        y={-34 - s / 2}
        width={s}
        height={s}
        opacity={o}
        clipPath={id.sceneClip ? 'url(#' + clipId + ')' : undefined}
        preserveAspectRatio={id.sceneClip ? 'xMidYMid slice' : undefined}
      />
    );
  }
  if (id.glyph === 'terminal') {
    return (
      <g transform="translate(-20 -54) scale(1.6667)" opacity={o}>
        <path d="M5 7l5 5-5 5M12 17h7" className="scene-terminal" />
      </g>
    );
  }
  return (
    <text x={0} y={-22} textAnchor="middle" opacity={o} className="scene-letter" style={{ fill: 'var(--agent-' + id.color + '-on)' }}>
      {id.letter}
    </text>
  );
}
