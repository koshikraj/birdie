import { AgentTile } from './AgentTile';

/**
 * Two agents' tiles stacked corner to corner, the first in front: who sent a
 * message and who got it, or the two sides of a thread. With one name it's a
 * single tile. Decorative: write the names next to it.
 */
export function TilePair({ a, b, size = 34, className }: { a: string; b?: string; size?: number; className?: string }) {
  return (
    <div className={'tile-pair tile-pair-stack' + (className ? ' ' + className : '')} style={{ width: b ? Math.round(size * 1.76) : size }}>
      <AgentTile name={a} size={size} ring />
      {b && <AgentTile name={b} size={size} ring style={{ marginLeft: -Math.round(size * 0.24), marginTop: Math.round(size * 0.3) }} />}
    </div>
  );
}
