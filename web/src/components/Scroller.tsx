import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';

interface Props {
  /** Names the region for screen readers and keyboard users. */
  label: string;
  /** Fit this many of the content's rows, then scroll. Rows are the children of the first child. */
  rows?: number;
  className?: string;
  style?: CSSProperties;
  children: ReactNode;
}

/**
 * A scrolling area with soft edges that show when there's more above or
 * below. It can size itself to a number of rows, like the live feed's four
 * messages, and remeasures as rows grow or shrink.
 */
export const Scroller = forwardRef<HTMLDivElement, Props>(function Scroller({ label, rows, className, style, children }, ref) {
  const box = useRef<HTMLDivElement>(null);
  useImperativeHandle(ref, () => box.current as HTMLDivElement);
  const [edges, setEdges] = useState({ top: false, bottom: false });
  const [fit, setFit] = useState<number | undefined>();
  // Mirrors of the state, so measuring only sets state when something changed.
  const seen = useRef({ top: false, bottom: false, fit: undefined as number | undefined });

  const look = useCallback(() => {
    const el = box.current;
    if (!el) return;
    const top = el.scrollTop > 2;
    const bottom = el.scrollTop + el.clientHeight < el.scrollHeight - 2;
    if (seen.current.top === top && seen.current.bottom === bottom) return;
    seen.current.top = top;
    seen.current.bottom = bottom;
    setEdges({ top, bottom });
  }, []);

  const measure = useCallback(() => {
    const el = box.current;
    const list = el?.firstElementChild as HTMLElement | null;
    if (!el || !list || !rows) return;
    const items = list.children;
    let h: number | undefined;
    if (items.length > rows) {
      const next = items[rows] as HTMLElement;
      h = Math.ceil(next.getBoundingClientRect().top - list.getBoundingClientRect().top);
    }
    // Small changes are rounding; ignoring them keeps the size from flickering between two values.
    const f = seen.current.fit;
    if (f !== h && !(f !== undefined && h !== undefined && Math.abs(f - h) <= 1)) {
      seen.current.fit = h;
      setFit(h);
    }
    look();
  }, [rows, look]);

  useLayoutEffect(measure);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => (rows ? measure() : look()));
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    return () => ro.disconnect();
  }, [rows, measure, look]);

  return (
    <div
      ref={box}
      role="region"
      aria-label={label}
      tabIndex={0}
      onScroll={look}
      className={'scroller' + (edges.top ? ' has-above' : '') + (edges.bottom ? ' has-below' : '') + (className ? ' ' + className : '')}
      style={fit !== undefined ? { ...style, maxHeight: fit } : style}
    >
      {children}
    </div>
  );
});
