import { useEffect, useId, useRef, useState } from 'react';
import { identity } from '../lib/agents';
import { useReducedMotion } from '../lib/media';
import { T, bump, inOut, seg } from '../scene/geometry';
import { BirdBody, TileClip, birdLook, burstBits } from '../scene/MorphBird';
import '../scene/scene.css';

/** The loop, in seconds: rest as the tile, pop into the bird, perch, turn back. */
const TILE = 1.8;
const BIRD = 2.6;
const LOOP = TILE + T.MI + BIRD + T.MO;
const IN = TILE;
const OUT = TILE + T.MI + BIRD;

function seed(name: string): number {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return (h % 1000) / 1000;
}

/** Where the loop is at u seconds in, with the same pop as the live scene. */
function frame(u: number) {
  if (u < IN) return { p: 0, mix: 0, hop: 0, pop: 1, burst: 0, still: true };
  if (u < IN + T.MI) {
    const q = seg(u, IN, IN + T.MI);
    const mix = inOut(q);
    return {
      p: mix + Math.sin(Math.PI * seg(q, 0.55, 1)) * 0.07,
      mix,
      hop: bump(u, IN + 0.05, 0.6) * 22,
      pop: 1 + Math.sin(Math.PI * q) * 0.08,
      burst: seg(u, IN + T.MI * 0.45, IN + T.MI * 0.45 + 0.6),
      still: false,
    };
  }
  if (u < OUT) {
    const burst = seg(u, IN + T.MI * 0.45, IN + T.MI * 0.45 + 0.6);
    return { p: 1, mix: 1, hop: bump(u, IN + 0.05, 0.6) * 22, pop: 1, burst, still: burst >= 1 && u > IN + 0.65 };
  }
  const mix = 1 - inOut(seg(u, OUT, OUT + T.MO));
  return { p: mix, mix, hop: 0, pop: 1, burst: 0, still: false };
}

/**
 * An agent's tile turning into its bird and back, over and over, as it does
 * in the live scene. Still as the bird under reduced motion. Decorative.
 */
export function LoopBird({ name, width = 84, className }: { name: string; width?: number; className?: string }) {
  const id = identity(name);
  const clipId = 'loop-tile-' + useId().replace(/[^a-zA-Z0-9]/g, '');
  const reduce = useReducedMotion();
  const box = useRef<SVGSVGElement>(null);
  const offset = useRef(seed(name) * LOOP);
  const [u, setU] = useState(() => offset.current % LOOP);

  useEffect(() => {
    if (reduce) return;
    let raf = 0;
    let visible = true;
    let wasStill = false;
    const start = performance.now();
    const tick = (ts: number) => {
      raf = requestAnimationFrame(tick);
      if (!visible) return;
      const next = ((ts - start) / 1000 + offset.current) % LOOP;
      const still = frame(next).still;
      if (still && wasStill) return;
      wasStill = still;
      setU(next);
    };
    const io = typeof IntersectionObserver !== 'undefined' && box.current
      ? new IntersectionObserver(([e]) => (visible = e.isIntersecting))
      : null;
    if (io && box.current) io.observe(box.current);
    raf = requestAnimationFrame(tick);
    return () => {
      cancelAnimationFrame(raf);
      io?.disconnect();
    };
  }, [reduce]);

  const f = reduce ? { p: 1, mix: 1, hop: 0, pop: 1, burst: 0, still: true } : frame(u);
  const look = birdLook(id, f.p, f.mix);
  const bits = burstBits(f.burst, 0, 0, seed(name) * 6);
  return (
    <svg ref={box} aria-hidden="true" viewBox="-62 -104 124 112" width={width} height={(width * 112) / 124} className={className} style={{ display: 'block', overflow: 'visible' }}>
      <defs>
        <TileClip id={clipId} />
      </defs>
      <rect x={-56} y={0} width={112} height={5} rx={2.5} className="scene-ink" />
      <g transform={'translate(0 ' + (-f.hop).toFixed(2) + ') scale(' + f.pop.toFixed(3) + ')'}>
        <BirdBody id={id} look={look} clipId={clipId} />
      </g>
      {bits.map((b, k) => (
        <circle key={k} cx={b.x.toFixed(1)} cy={b.y.toFixed(1)} r={b.r.toFixed(2)} opacity={b.o.toFixed(2)} style={{ fill: 'var(--agent-' + id.color + ')' }} />
      ))}
    </svg>
  );
}
