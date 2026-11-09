/**
 * You, drawn like the birds: flat shapes, a round white eye with a pupil,
 * and an ink line for the mouth. Fills a square; the tile behind it is the
 * caller's.
 */
export function YouFace({ size }: { size: number }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 48 48" width={size} height={size} style={{ display: 'block' }}>
      {/* Shoulders */}
      <path d="M8 48 C8 39 15 35 24 35 C33 35 40 39 40 48 Z" style={{ fill: 'var(--you-shirt)' }} />
      {/* Ears and head */}
      <circle cx={12.5} cy={24} r={3.4} style={{ fill: 'var(--you-skin)' }} />
      <circle cx={35.5} cy={24} r={3.4} style={{ fill: 'var(--you-skin)' }} />
      <rect x={13} y={10} width={22} height={26} rx={11} style={{ fill: 'var(--you-skin)' }} />
      {/* Hair, swept like the birds' tails */}
      <path d="M12.5 21 C12 12 17 7.5 24 7.5 C31.5 7.5 36.5 11.5 35.5 20 L33 17 C29 18 22 17 18 13.5 C17 16.5 15 18.5 12.5 21 Z" style={{ fill: 'var(--you-ink)' }} />
      {/* Eyes */}
      <circle cx={19.5} cy={23} r={3.4} style={{ fill: 'var(--bird-eye)' }} />
      <circle cx={20.6} cy={23} r={1.7} style={{ fill: 'var(--bird-pupil)' }} />
      <circle cx={28.5} cy={23} r={3.4} style={{ fill: 'var(--bird-eye)' }} />
      <circle cx={29.6} cy={23} r={1.7} style={{ fill: 'var(--bird-pupil)' }} />
      {/* Smile */}
      <path d="M20.5 29.5 Q24 32.5 27.5 29.5" fill="none" strokeWidth={1.8} strokeLinecap="round" style={{ stroke: 'var(--you-ink)' }} />
    </svg>
  );
}
