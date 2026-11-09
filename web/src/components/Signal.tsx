/**
 * The logo's small icon, a dot and an upright dash, blinking in turn. It's
 * the app's loader and "something is moving" mark. Always pass a label: the
 * marks alone don't say what's happening.
 */
export function Signal({ label, live = true, size, className }: { label: string; live?: boolean; size?: number; className?: string }) {
  return (
    <span className={'signal' + (live ? ' signal-live' : '') + (className ? ' ' + className : '')} role="status">
      <SignalMark size={size} />
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

/** A panel's loading state: the signal, centred, with room around it. */
export function Loading({ label }: { label: string }) {
  return (
    <div className="loading">
      <Signal label={label} size={26} />
    </div>
  );
}

/** Rows standing in for a list that's still loading. Hidden from screen readers; pair it with a Signal or Loading. */
export function Skeleton({ rows = 3, icon = true }: { rows?: number; icon?: boolean }) {
  return (
    <div className="skeleton" aria-hidden="true">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="skeleton-row" style={{ animationDelay: i * 0.12 + 's' }}>
          {icon && <span className="skeleton-icon" />}
          <span className="skeleton-lines">
            <span style={{ width: 40 + ((i * 23) % 35) + '%' }} />
            <span style={{ width: 70 + ((i * 17) % 25) + '%' }} />
          </span>
        </div>
      ))}
    </div>
  );
}

/** A chat bubble with the signal in it, for an agent that's working on an answer. */
export function Typing({ label }: { label: string }) {
  return (
    <span className="typing" role="status">
      <span className="typing-bubble">
        <SignalMark size={22} />
      </span>
      <span className="typing-text">{label}</span>
    </span>
  );
}
