import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { AgentTile } from './AgentTile';

/** Where a run starts and lands, in viewport coordinates. */
export interface Run {
  id: string;
  from: string;
  to: string;
  /** The hub's center. */
  start: { x: number; y: number };
  /** Where the new card's first icon will sit, its top left corner. */
  land: () => { x: number; y: number };
}

const FLY = 900;
const SPLIT = 420;
const TILE = 34;
/** The second icon's offset in a feed card's pair. */
const PAIR = { x: 26, y: 10 };
const BIRD = 64;

/** A point on the quadratic curve a, c, b. */
function quad(a: number, c: number, b: number, t: number) {
  return (1 - t) * (1 - t) * a + 2 * (1 - t) * t * c + t * t * b;
}

/**
 * The birdie flies a message from the hub to the top of the feed, then turns
 * into the message's two icons right where its card's icons go. onLand runs
 * as the bird comes in, so the card can open under it; onDone when the icons
 * have settled and the card's own icons can take over.
 */
export function CourierFlight({ run, onLand, onDone }: { run: Run; onLand: () => void; onDone: () => void }) {
  const bird = useRef<HTMLDivElement>(null);
  const a = useRef<HTMLDivElement>(null);
  const b = useRef<HTMLDivElement>(null);
  const bits = useRef<HTMLDivElement>(null);
  const [split, setSplit] = useState<{ x: number; y: number; land: { x: number; y: number } } | null>(null);
  const done = useRef({ onLand, onDone });
  done.current = { onLand, onDone };

  // The flight: an arc up and over, flapping, a little smaller as it nears the feed.
  useEffect(() => {
    const el = bird.current;
    if (!el) return;
    const land = run.land();
    const end = { x: land.x + (TILE + PAIR.x) / 2, y: land.y + (TILE + PAIR.y) / 2 };
    const s = run.start;
    const c = { x: (s.x + end.x) / 2, y: Math.min(s.y, end.y) - 140 };
    const face = end.x >= s.x ? 1 : -1;
    const frames: Keyframe[] = [];
    const N = 24;
    for (let i = 0; i <= N; i++) {
      const t = i / N;
      const x = quad(s.x, c.x, end.x, t);
      const y = quad(s.y, c.y, end.y, t);
      const dx = quad(s.x, c.x, end.x, Math.min(1, t + 0.02)) - quad(s.x, c.x, end.x, Math.max(0, t - 0.02));
      const dy = quad(s.y, c.y, end.y, Math.min(1, t + 0.02)) - quad(s.y, c.y, end.y, Math.max(0, t - 0.02));
      const tilt = Math.max(-28, Math.min(28, (Math.atan2(dy, Math.abs(dx) || 0.001) * 180) / Math.PI));
      // Pops out of the hub, then shrinks toward the card's icons.
      const scale = t < 0.15 ? 0.4 + (t / 0.15) * 0.8 : 1.2 - (t - 0.15) * 0.6;
      frames.push({
        transform: 'translate(' + (x - BIRD / 2).toFixed(1) + 'px,' + (y - BIRD / 2).toFixed(1) + 'px) rotate(' + (tilt * face).toFixed(1) + 'deg) scale(' + (scale * face).toFixed(3) + ',' + scale.toFixed(3) + ')',
        opacity: t < 0.08 ? t / 0.08 : 1,
      });
    }
    const anim = el.animate(frames, { duration: FLY, easing: 'cubic-bezier(0.45, 0, 0.3, 1)', fill: 'forwards' });
    anim.onfinish = () => setSplit({ x: end.x, y: end.y, land: run.land() });
    // The card's slot opens as the bird comes in, so it lands in the gap.
    const open = window.setTimeout(() => done.current.onLand(), FLY * 0.55);
    return () => {
      anim.cancel();
      window.clearTimeout(open);
    };
  }, [run]);

  // The split: the bird pops, and the two icons spring out to their places in the card.
  useEffect(() => {
    if (!split) return;
    const pop = { duration: SPLIT, easing: 'cubic-bezier(0.34, 1.56, 0.64, 1)', fill: 'forwards' as const };
    const from = (el: HTMLDivElement | null, to: { x: number; y: number }, delay: number) =>
      el?.animate(
        [
          { transform: 'translate(' + (split.x - TILE / 2) + 'px,' + (split.y - TILE / 2) + 'px) scale(0.2)', opacity: 0 },
          { transform: 'translate(' + to.x + 'px,' + to.y + 'px) scale(1)', opacity: 1 },
        ],
        { ...pop, delay, fill: 'both' },
      );
    bird.current?.animate(
      [
        { transform: 'translate(' + (split.x - BIRD / 2) + 'px,' + (split.y - BIRD / 2) + 'px) scale(0.6)', opacity: 1 },
        { transform: 'translate(' + (split.x - BIRD / 2) + 'px,' + (split.y - BIRD / 2) + 'px) scale(0)', opacity: 0 },
      ],
      { duration: 220, easing: 'ease-in', fill: 'forwards' },
    );
    bits.current?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 520, fill: 'forwards' });
    Array.from(bits.current?.children ?? []).forEach((d, k) => {
      const ang = (k / 6) * Math.PI * 2;
      (d as HTMLElement).animate(
        [
          { transform: 'translate(' + split.x + 'px,' + split.y + 'px) scale(1)' },
          { transform: 'translate(' + (split.x + Math.cos(ang) * 40) + 'px,' + (split.y + Math.sin(ang) * 40) + 'px) scale(0.2)' },
        ],
        { duration: 520, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)', fill: 'forwards' },
      );
    });
    const l = split.land;
    from(a.current, l, 60);
    const last = from(b.current, { x: l.x + PAIR.x, y: l.y + PAIR.y }, 140);
    const t = window.setTimeout(() => done.current.onDone(), SPLIT + 160);
    return () => {
      window.clearTimeout(t);
      last?.cancel();
    };
  }, [split]);

  return createPortal(
    <div className="courier" aria-hidden="true">
      <div ref={bird} className="courier-bird">
        <CourierBird />
      </div>
      {split && (
        <>
          <div ref={bits} className="courier-bits">
            {[0, 1, 2, 3, 4, 5].map((k) => (
              <span key={k} className={k % 2 ? 'courier-bit courier-bit-dash' : 'courier-bit'} />
            ))}
          </div>
          <div ref={b} className="courier-tile">
            <AgentTile name={run.to} size={TILE} ring />
          </div>
          <div ref={a} className="courier-tile courier-tile-front">
            <AgentTile name={run.from} size={TILE} ring />
          </div>
        </>
      )}
    </div>,
    document.body,
  );
}

/** The logo's dot bird, letter in its beak, wing flapping. */
function CourierBird() {
  return (
    <svg viewBox="0 18 84 72" width={BIRD} height={(BIRD * 72) / 84}>
      <path d="M18 50 L2 46 L16 68 Z" style={{ fill: 'var(--bird-dot)' }} />
      <circle cx={38} cy={62} r={24} style={{ fill: 'var(--bird-dot)' }} />
      <ellipse cx={30} cy={60} rx={13} ry={8} className="courier-wing" style={{ fill: 'var(--agent-1-wing)' }} />
      <path d="M59 54 L72 60 L59 66 Z" style={{ fill: 'var(--bird-line)' }} />
      <circle cx={46} cy={54} r={6.5} fill="#FFFFFF" />
      <circle cx={48} cy={54} r={3.2} fill="#0A2E2B" />
      <g transform="rotate(-8 74 52)">
        <rect x={62} y={44} width={22} height={16} rx={2} style={{ fill: 'var(--letter-edge)' }} />
        <rect x={63.5} y={45.5} width={19} height={13} rx={1} style={{ fill: 'var(--letter)' }} />
        <path d="M63.5 46 L73 53 L82.5 46 L82.5 49 L73 56 L63.5 49 Z" style={{ fill: 'var(--signal)' }} />
      </g>
    </svg>
  );
}
