import type { BirdSpec } from '../lib/agents';

/**
 * Each bird's parts, drawn facing right with its feet at 0,0: the geometry of
 * the site's birds (Dot, Dash, the arch bird and the box bird).
 */
export interface Spec {
  w: number;
  h: number;
  rx: number;
  tail: [number, number];
  tailPts: string;
  beak: [number, number];
  beakPts: string;
  eye: [number, number];
  /** cx, cy, rotation. */
  wing: [number, number, number];
  wingR: [number, number];
}

export const SPECS: Record<BirdSpec, Spec> = {
  dot: {
    w: 64, h: 64, rx: 32,
    tail: [-28, -48], tailPts: '-28,-48 -50,-56 -30,-20',
    beak: [30, -42], beakPts: '30,-42 46,-34 30,-26',
    eye: [12, -44], wing: [-12, -26, -20], wingR: [14, 10],
  },
  dash: {
    w: 40, h: 84, rx: 20,
    tail: [-16, -36], tailPts: '-16,-36 -36,-10 -16,-6',
    beak: [19, -66], beakPts: '19,-66 35,-58 19,-50',
    eye: [6, -62], wing: [-4, -30, 0], wingR: [9, 16],
  },
  arch: {
    w: 54, h: 70, rx: 27,
    tail: [-24, -44], tailPts: '-24,-44 -42,-56 -26,-16',
    beak: [25, -50], beakPts: '25,-50 40,-42 25,-34',
    eye: [10, -48], wing: [-8, -26, 8], wingR: [11, 14],
  },
  box: {
    w: 62, h: 58, rx: 18,
    tail: [-28, -36], tailPts: '-28,-36 -46,-48 -30,-12',
    beak: [29, -38], beakPts: '29,-38 44,-30 29,-22',
    eye: [12, -36], wing: [-10, -20, -15], wingR: [13, 10],
  },
};

export type Pt = [number, number];

/** A cubic bezier, sampled so a letter can move along it at an even speed. */
export interface Lut {
  pts: Pt[];
  cum: number[];
  len: number;
}

const STEPS = 48;

export function lut(p: [Pt, Pt, Pt, Pt]): Lut {
  const pts: Pt[] = [];
  const cum = [0];
  for (let i = 0; i <= STEPS; i++) {
    const t = i / STEPS;
    const u = 1 - t;
    pts.push([
      u * u * u * p[0][0] + 3 * u * u * t * p[1][0] + 3 * u * t * t * p[2][0] + t * t * t * p[3][0],
      u * u * u * p[0][1] + 3 * u * u * t * p[1][1] + 3 * u * t * t * p[2][1] + t * t * t * p[3][1],
    ]);
    if (i > 0) cum.push(cum[i - 1] + Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]));
  }
  return { pts, cum, len: cum[STEPS] };
}

export function at(L: Lut, frac: number): Pt {
  const d = Math.max(0, Math.min(1, frac)) * L.len;
  let i = 1;
  while (i < STEPS && L.cum[i] < d) i++;
  const a = L.pts[i - 1];
  const b = L.pts[i];
  const span = L.cum[i] - L.cum[i - 1] || 1;
  const k = (d - L.cum[i - 1]) / span;
  return [a[0] + (b[0] - a[0]) * k, a[1] + (b[1] - a[1]) * k];
}

export const clamp = (v: number) => (v < 0 ? 0 : v > 1 ? 1 : v);
export const seg = (t: number, a: number, b: number) => clamp((t - a) / (b - a));
export const lerp = (a: number, b: number, p: number) => a + (b - a) * p;
export const inOut = (p: number) => (p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2);
export const outCubic = (p: number) => 1 - Math.pow(1 - p, 3);
export const back = (p: number, c = 2.2) => (p >= 1 ? 1 : 1 + (c + 1) * Math.pow(p - 1, 3) + c * Math.pow(p - 1, 2));
export const bump = (t: number, start: number, dur: number) => {
  const p = seg(t, start, start + dur);
  return p > 0 && p < 1 ? Math.sin(Math.PI * p) : 0;
};

/** Scales a part about its own anchor; hidden when it's too small to see. */
export function part(ox: number, oy: number, s: number, extra = ''): string {
  if (s <= 0.01) return 'scale(0)';
  return 'translate(' + ox + ' ' + oy + ') scale(' + s.toFixed(3) + ') translate(' + -ox + ' ' + -oy + ')' + extra;
}

/** One message's timeline, in seconds, from the approved scene. */
export const T = {
  C: 4.6,
  MI: 0.7,
  MO: 0.6,
  L1: [0.45, 1.55] as const,
  HUB: 1.55,
  L2: [1.75, 2.85] as const,
  ARRIVE: 2.85,
  RIN: 1.6,
  SOUT: 3.0,
  ROUT: 3.6,
  FADE: [3.9, 4.4] as const,
  STILL: 1.1,
  DOZE: 0.4,
};
