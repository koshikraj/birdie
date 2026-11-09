import type { Identity } from '../lib/agents';
import { SPECS, clamp, lerp, outCubic, part, seg, type Spec } from './geometry';

const f2 = (n: number) => n.toFixed(2);

/** The tile's clip, in a bird's own coordinates, for icons that fill their tile. */
export function TileClip({ id }: { id: string }) {
  return (
    <clipPath id={id}>
      <rect x={-34} y={-68} width={68} height={68} rx={17} />
    </clipPath>
  );
}

export interface BirdLook {
  sp: Spec;
  w: number;
  h: number;
  rx: number;
  fill: string;
  parts: number;
  eyes: number;
  glyph: number;
  wingExtra: string;
}

/**
 * How an agent looks between its tile (p = 0) and its bird (p = 1). p may
 * overshoot a little as the bird pops in; mix is how far the color has gone.
 */
export function birdLook(id: Identity, p: number, mix: number): BirdLook {
  const sp = SPECS[id.spec];
  const w = Math.max(10, lerp(68, sp.w, p));
  const h = Math.max(10, lerp(68, sp.h, p));
  const rx = Math.min(Math.max(lerp(17, sp.rx, p), 4), w / 2, h / 2);
  const m = clamp(mix);
  const birdFill = 'var(--agent-' + id.color + ')';
  const fill = m >= 0.995 ? birdFill : m <= 0.005 ? id.tile : 'color-mix(in oklab, ' + birdFill + ' ' + (m * 100).toFixed(1) + '%, ' + id.tile + ')';
  return {
    sp, w, h, rx, fill,
    parts: clamp((p - 0.45) / 0.55),
    eyes: clamp((p - 0.6) / 0.4),
    glyph: 1 - seg(m, 0.05, 0.5),
    wingExtra: sp.wing[2] ? ' rotate(' + sp.wing[2] + ' ' + sp.wing[0] + ' ' + sp.wing[1] + ')' : '',
  };
}

/** The bird or its tile, feet at 0,0, facing right when face is 1. */
export function BirdBody({ id, look, face = 1, shut = false, clipId }: { id: Identity; look: BirdLook; face?: number; shut?: boolean; clipId: string }) {
  const { sp } = look;
  return (
    <>
      <g transform={'scale(' + face + ' 1)'}>
        <polygon points={sp.tailPts} transform={part(sp.tail[0], sp.tail[1], look.parts)} style={{ fill: 'var(--agent-' + id.color + ')' }} />
        <rect x={f2(-look.w / 2)} y={f2(-look.h)} width={f2(look.w)} height={f2(look.h)} rx={f2(look.rx)} style={{ fill: look.fill }} />
        <ellipse
          cx={sp.wing[0]} cy={sp.wing[1]} rx={sp.wingR[0]} ry={sp.wingR[1]}
          transform={part(sp.wing[0], sp.wing[1], look.parts, look.wingExtra)}
          style={{ fill: 'var(--agent-' + id.color + '-wing)' }}
        />
        <polygon points={sp.beakPts} transform={part(sp.beak[0], sp.beak[1] + 8, look.parts)} className="scene-ink" />
        <circle cx={sp.eye[0]} cy={sp.eye[1]} r={9} transform={part(sp.eye[0], sp.eye[1], shut ? 0 : look.eyes)} style={{ fill: 'var(--bird-eye)' }} />
        <circle cx={sp.eye[0] + 3} cy={sp.eye[1]} r={4.5} transform={part(sp.eye[0] + 3, sp.eye[1], shut ? 0 : look.eyes)} style={{ fill: 'var(--bird-pupil)' }} />
        <rect x={sp.eye[0] - 8} y={sp.eye[1] - 2} width={16} height={4} rx={2} transform={part(sp.eye[0], sp.eye[1], shut ? look.eyes : 0)} style={{ fill: 'var(--bird-eye)' }} />
      </g>
      {look.glyph > 0.001 && <Glyph id={id} clipId={clipId} opacity={look.glyph} />}
    </>
  );
}

export interface Bit {
  x: number;
  y: number;
  r: number;
  o: number;
}

export const NO_BIT: Bit = { x: 0, y: 0, r: 0, o: 0 };

/** The six bits a bird throws off as it pops in, q from 0 to 1 after the pop. */
export function burstBits(q: number, x: number, y: number, turn: number): Bit[] {
  if (q <= 0 || q >= 1) return Array.from({ length: 6 }, () => NO_BIT);
  return Array.from({ length: 6 }, (_, k) => {
    const ang = (k / 6) * Math.PI * 2 + turn;
    const d = outCubic(q) * 74;
    return { x: x + Math.cos(ang) * d, y: y - 46 + Math.sin(ang) * d + q * q * 26, r: 6 * (1 - q) + 0.5, o: 1 - q };
  });
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
