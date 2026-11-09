import type { CSSProperties } from 'react';
import type { AgentColor, BirdSpec } from '../lib/agents';
import { SPECS } from '../scene/geometry';

interface Props {
  spec: BirdSpec;
  color: AgentColor;
  asleep?: boolean;
  width?: number;
  className?: string;
  style?: CSSProperties;
}

/** An agent's bird on its wire, drawn in theme colors. Decorative. */
export function BirdShape({ spec, color, asleep, width = 76, className, style }: Props) {
  const s = SPECS[spec];
  const fill = 'var(--agent-' + color + ')';
  const wing = 'var(--agent-' + color + '-wing)';
  return (
    <svg
      aria-hidden="true"
      viewBox="-60 -96 120 104"
      width={width}
      height={(width * 104) / 120}
      className={className}
      style={{ display: 'block', ...style }}
    >
      <rect x={-56} y={0} width={112} height={6} rx={3} style={{ fill: 'var(--bird-line)' }} />
      <polygon points={s.tailPts} style={{ fill }} />
      <rect x={-s.w / 2} y={-s.h} width={s.w} height={s.h} rx={s.rx} style={{ fill }} />
      <ellipse
        cx={s.wing[0]}
        cy={s.wing[1]}
        rx={s.wingR[0]}
        ry={s.wingR[1]}
        transform={s.wing[2] ? 'rotate(' + s.wing[2] + ' ' + s.wing[0] + ' ' + s.wing[1] + ')' : undefined}
        style={{ fill: wing }}
      />
      <polygon points={s.beakPts} style={{ fill: 'var(--bird-line)' }} />
      {asleep ? (
        <rect x={s.eye[0] - 8} y={s.eye[1] - 2} width={16} height={4} rx={2} style={{ fill: 'var(--bird-eye)' }} />
      ) : (
        <>
          <circle cx={s.eye[0]} cy={s.eye[1]} r={9} style={{ fill: 'var(--bird-eye)' }} />
          <circle cx={s.eye[0] + 3} cy={s.eye[1]} r={4.5} style={{ fill: 'var(--bird-pupil)' }} />
        </>
      )}
    </svg>
  );
}
