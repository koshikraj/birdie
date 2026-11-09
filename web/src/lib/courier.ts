/**
 * Hands a message from the live scene to the live messages feed. While the
 * scene is animating, the feed holds each new message back until its letter
 * passes the birdie hub. Then a bird flies the message over, and the feed's
 * card pops in where it lands.
 */
export interface HubPass {
  id: string;
  from: string;
  to: string;
  /** The hub, in viewport coordinates, when the letter passed it. */
  rect: DOMRect;
}

type Fn<T> = (v: T) => void;

function channel<T>() {
  const fns = new Set<Fn<T>>();
  return {
    send: (v: T) => fns.forEach((f) => f(v)),
    on: (f: Fn<T>) => {
      fns.add(f);
      return () => {
        fns.delete(f);
      };
    },
  };
}

const passes = channel<HubPass>();
const drops = channel<string>();
const changes = channel<boolean>();
let active = false;

export const courier = {
  /** Whether the scene is animating letters, so the feed should wait for them. */
  get active() {
    return active;
  },
  setActive(next: boolean) {
    if (next === active) return;
    active = next;
    changes.send(next);
  },
  /** The scene: a letter passed the hub. */
  passed: passes.send,
  /** The scene: a letter won't play, so its message shouldn't wait for it. */
  dropped: drops.send,
  onPass: passes.on,
  onDrop: drops.on,
  onActive: changes.on,
};
