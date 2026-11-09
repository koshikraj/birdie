import type { CSSProperties } from 'react';
import { identity } from '../lib/agents';
import { YouFace } from './YouFace';

interface Props {
  /** The agent's bus name. */
  name: string;
  size?: number;
  /** Draws a ring in the surface color, for overlapping tiles. */
  ring?: boolean;
  className?: string;
  style?: CSSProperties;
}

/** An agent's product icon on its tile. Decorative: the name is always written next to it. */
export function AgentTile({ name, size = 34, ring, className, style }: Props) {
  const id = identity(name);
  const radius = Math.round(size * 0.29);
  return (
    <span
      aria-hidden="true"
      className={'agent-tile' + (ring ? ' agent-tile-ring' : '') + (className ? ' ' + className : '')}
      style={{ width: size, height: size, borderRadius: radius, background: id.tile, ...style }}
    >
      {id.glyph === 'face' ? (
        <YouFace size={size} />
      ) : id.glyph === 'icon' && id.icon ? (
        <img src={id.icon} alt="" style={id.full ? { width: '100%', height: '100%', objectFit: 'cover' } : { width: '56%', height: '56%' }} />
      ) : id.glyph === 'terminal' ? (
        <svg viewBox="0 0 24 24" width={Math.round(size * 0.6)} height={Math.round(size * 0.6)} className="agent-tile-terminal">
          <path d="M5 7l5 5-5 5M12 17h7" />
        </svg>
      ) : (
        <span className="agent-tile-letter" style={{ fontSize: Math.round(size * 0.46), color: 'var(--agent-' + id.color + '-on)' }}>
          {id.letter}
        </span>
      )}
    </span>
  );
}
