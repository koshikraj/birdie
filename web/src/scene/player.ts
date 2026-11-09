import { T, inOut, seg } from './geometry';

/** One message's trip across the scene. */
export interface Flight {
  id: string;
  from: string;
  to: string;
  fromLabel: string;
  toLabel: string;
  text: string;
  tone: 'request' | 'reply';
  /** The recipient took it (claimed or delivered). */
  landed: boolean;
  /** A try failed, or birdie gave up. */
  failed: boolean;
}

interface Morph {
  from: number;
  to: number;
  start: number;
  dur: number;
  /** Pops with feathers and a hop. Dozing off doesn't. */
  burst: boolean;
}

export interface AgentAnim {
  morph: Morph;
  shut: boolean;
  wakeAt: number;
}

export interface Current {
  f: Flight;
  /** Seconds into the flight's timeline. Stops just before arrival until the recipient takes it. */
  t: number;
  /** Seconds since the flight started, including any wait. */
  elapsed: number;
  waited: number;
}

/** What the player needs to know about the scene each frame. */
export interface SceneContext {
  placed: Set<string>;
  /** Asleep by the data: quiet for 5 minutes, nothing busy, no delivery error. */
  sleeping: Set<string>;
  /** The agent under the pointer, shown as its icon whatever it's doing. */
  hovered?: string | null;
}

/** Pending flights beyond this are dropped, oldest first, so a burst doesn't back up. */
const MAX_PENDING = 3;
/** How long a letter waits at the recipient before it lands anyway. */
const HOLD_MAX = 4;
const HOLD_AT = T.ARRIVE - 0.001;

export class Player {
  clock = 0;
  queue: Flight[] = [];
  current: Current | null = null;
  last: Flight | null = null;
  anims = new Map<string, AgentAnim>();
  paused = false;
  reduce = false;
  /** Called when a flight starts playing. */
  onStart: ((f: Flight) => void) | null = null;

  push(f: Flight) {
    if (this.reduce) {
      // No motion: show the newest message as a still, mid-wire.
      this.current = { f, t: T.STILL, elapsed: T.STILL, waited: 0 };
      this.last = f;
      this.onStart?.(f);
      return;
    }
    if (this.current?.f.id === f.id || this.queue.some((q) => q.id === f.id)) return;
    this.queue.push(f);
    while (this.queue.length > MAX_PENDING) this.queue.shift();
  }

  /** Records how far a message got, for a flight that's playing or waiting. */
  update(id: string, how: 'landed' | 'failed') {
    const all = this.current ? [this.current.f, ...this.queue] : this.queue;
    const f = all.find((x) => x.id === id) || (this.last?.id === id ? this.last : undefined);
    if (!f) return;
    if (how === 'landed') f.landed = true;
    else f.failed = true;
  }

  setReduce(reduce: boolean) {
    this.reduce = reduce;
    if (reduce) {
      const f = this.current?.f || this.queue[this.queue.length - 1] || this.last;
      this.queue = [];
      this.current = f ? { f, t: T.STILL, elapsed: T.STILL, waited: 0 } : null;
    }
    // Turning motion back on lets a still finish its trip from mid-wire.
  }

  /** Something is moving or about to. */
  active(): boolean {
    if (this.reduce || this.paused) return false;
    if (this.current || this.queue.length) return true;
    for (const a of this.anims.values()) {
      const m = a.morph;
      if (m.dur > 0 && this.clock < m.start + m.dur + 0.7) return true;
      if (this.clock < a.wakeAt + 0.6) return true;
    }
    return false;
  }

  /** Advances by dt seconds, then retargets every bird. */
  step(dt: number, ctx: SceneContext) {
    if (!this.reduce && !this.paused) {
      const speed = 1 + 0.35 * Math.min(this.queue.length, 3);
      const d = dt * speed;
      this.clock += d;
      if (!this.current && this.queue.length) {
        this.current = { f: this.queue.shift() as Flight, t: 0, elapsed: 0, waited: 0 };
        this.onStart?.(this.current.f);
      }
      const c = this.current;
      if (c) {
        c.elapsed += d;
        const release = c.f.landed || c.f.failed || c.waited >= HOLD_MAX || !ctx.placed.has(c.f.to);
        if (c.t < HOLD_AT) c.t = release ? c.t + d : Math.min(c.t + d, HOLD_AT);
        else if (!release) c.waited += d;
        else c.t += d;
        if (c.t >= T.C) {
          this.last = c.f;
          this.current = null;
        }
      }
    }
    this.retarget(ctx);
  }

  private retarget(ctx: SceneContext) {
    const c = this.current;
    const next = this.queue[0];
    const inNext = (n: string) => !!next && (next.from === n || next.to === n);
    for (const name of ctx.placed) {
      let target = 0;
      let involved = false;
      if (c && name === c.f.from) {
        involved = true;
        target = c.t < T.SOUT || inNext(name) ? 1 : 0;
      } else if (c && name === c.f.to) {
        involved = c.t >= T.RIN;
        target = c.t >= T.RIN && (c.t < T.ROUT || inNext(name)) ? 1 : 0;
      }
      const asleep = ctx.sleeping.has(name) && !involved;
      if (asleep) target = 1;
      if (name === ctx.hovered) target = 0;
      this.morphTo(name, target, !asleep && name !== ctx.hovered, asleep);
    }
    for (const name of [...this.anims.keys()]) if (!ctx.placed.has(name)) this.anims.delete(name);
  }

  private morphTo(name: string, target: number, burst: boolean, shut: boolean) {
    let a = this.anims.get(name);
    if (!a) {
      // A bird that just took its perch starts in its resting state.
      a = { morph: { from: target, to: target, start: -99, dur: 0, burst: false }, shut, wakeAt: -99 };
      this.anims.set(name, a);
      return;
    }
    if (a.morph.to !== target) {
      const from = this.morphValue(a.morph).color;
      a.morph = { from, to: target, start: this.clock, dur: this.reduce || this.paused ? 0 : target ? T.MI : T.MO, burst: burst && !this.reduce && !this.paused };
    }
    if (a.shut && !shut && !this.reduce) a.wakeAt = this.clock;
    a.shut = shut;
  }

  /** How far a bird is between its tile (0) and its bird (1). p overshoots a little as it pops in. */
  morphValue(m: Morph): { p: number; color: number; q: number } {
    const q = m.dur > 0 ? seg(this.clock, m.start, m.start + m.dur) : 1;
    const color = m.from + (m.to - m.from) * inOut(q);
    const p = color + (m.to === 1 && m.burst ? Math.sin(Math.PI * seg(q, 0.55, 1)) * 0.07 : 0);
    return { p, color, q };
  }
}
