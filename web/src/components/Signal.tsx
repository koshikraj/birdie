/**
 * The logo's small icon, a dot and an upright dash, blinking in turn. It's
 * the app's loader and "something is moving" mark. Always pass a label: the
 * marks alone don't say what's happening.
 */
export function Signal({ label, live = true, className }: { label: string; live?: boolean; className?: string }) {
  return (
    <span className={'signal' + (live ? ' signal-live' : '') + (className ? ' ' + className : '')} role="status">
      <SignalMark />
      <span className="signal-text">{label}</span>
    </span>
  );
}

export function SignalMark({ size = 18 }: { size?: number }) {
  return (
    <svg aria-hidden="true" viewBox="2 3 21 18" width={size} height={(size * 18) / 21} className="signal-mark">
      <circle cx={9} cy={14} r={5.5} className="signal-dot" />
      <rect x={15} y={4.5} width={6.5} height={15} rx={3.25} className="signal-dash" />
    </svg>
  );
}
