import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';

export interface SelectOption {
  value: string;
  label: string;
  /** A second, quieter line, such as an address. */
  hint?: string;
  /** Drawn before the label, such as an agent's tile. Decorative. */
  icon?: ReactNode;
}

interface Props {
  id?: string;
  /** The id of the element that names this control, usually its field label. */
  labelledBy: string;
  value: string;
  options: SelectOption[];
  onChange: (value: string) => void;
  /** Shown when nothing is picked or there are no options. */
  placeholder?: string;
  disabled?: boolean;
  describedBy?: string;
}

/**
 * A dropdown in the design system's style: a select-only combobox with a
 * listbox, following the WAI-ARIA pattern. Focus stays on the button; arrow
 * keys move through the options, Enter or Space picks, Escape closes, and
 * typing jumps to an option starting with those letters.
 */
export function Select({ id, labelledBy, value, options, onChange, placeholder = 'Pick one', disabled, describedBy }: Props) {
  const auto = useId();
  const base = id || 'select' + auto.replace(/:/g, '');
  const listId = base + '-list';
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const typed = useRef({ text: '', at: 0 });

  const current = options.findIndex((o) => o.value === value);
  const picked = current >= 0 ? options[current] : undefined;
  const off = disabled || !options.length;

  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('pointerdown', close);
    return () => document.removeEventListener('pointerdown', close);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    list.current?.querySelector<HTMLElement>('[data-index="' + active + '"]')?.scrollIntoView({ block: 'nearest' });
  }, [open, active]);

  const show = () => {
    if (off) return;
    setActive(current >= 0 ? current : 0);
    setOpen(true);
  };
  const pick = (i: number) => {
    const o = options[i];
    if (o) onChange(o.value);
    setOpen(false);
  };

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (off) return;
    const last = options.length - 1;
    const move = (i: number) => {
      e.preventDefault();
      if (!open) setOpen(true);
      setActive(Math.max(0, Math.min(last, i)));
    };
    switch (e.key) {
      case 'ArrowDown':
        return open ? move(active + 1) : (e.preventDefault(), show());
      case 'ArrowUp':
        return open ? move(active - 1) : (e.preventDefault(), show());
      case 'Home':
        return move(0);
      case 'End':
        return move(last);
      case 'PageDown':
        return move(active + 8);
      case 'PageUp':
        return move(active - 8);
      case 'Enter':
      case ' ':
        e.preventDefault();
        return open ? pick(active) : show();
      case 'Escape':
        if (open) {
          e.preventDefault();
          setOpen(false);
        }
        return;
      case 'Tab':
        if (open) pick(active);
        return;
    }
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const t = typed.current;
      const now = Date.now();
      t.text = (now - t.at > 700 ? '' : t.text) + e.key.toLowerCase();
      t.at = now;
      const from = open ? active : current;
      const order = options.map((_, i) => (from + 1 + i) % options.length);
      if (t.text.length > 1) order.unshift(from);
      const hit = order.find((i) => options[i].label.toLowerCase().startsWith(t.text));
      if (hit === undefined) return;
      if (open) setActive(hit);
      else onChange(options[hit].value);
    }
  };

  return (
    <div ref={root} className={'dd' + (open ? ' is-open' : '')}>
      <div
        id={base}
        role="combobox"
        tabIndex={off ? -1 : 0}
        className="dd-button"
        aria-labelledby={labelledBy + ' ' + base}
        aria-describedby={describedBy}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listId}
        aria-activedescendant={open ? listId + '-' + active : undefined}
        aria-disabled={off || undefined}
        onClick={() => (open ? setOpen(false) : show())}
        onKeyDown={onKey}
      >
        {picked ? <Choice option={picked} /> : <span className="dd-placeholder">{placeholder}</span>}
        <svg className="dd-chevron" aria-hidden="true" viewBox="0 0 16 16" width="16" height="16">
          <path d="M3.5 6l4.5 4.5L12.5 6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </div>
      <ul ref={list} id={listId} role="listbox" aria-labelledby={labelledBy} className="dd-list" hidden={!open} tabIndex={-1}>
        {options.map((o, i) => (
          <li
            key={o.value}
            id={listId + '-' + i}
            data-index={i}
            role="option"
            aria-selected={i === current}
            className={'dd-option' + (i === active ? ' is-active' : '')}
            onPointerMove={() => i !== active && setActive(i)}
            onPointerDown={(e) => e.preventDefault()}
            onClick={() => pick(i)}
          >
            <Choice option={o} />
            <svg className="dd-check" aria-hidden="true" viewBox="0 0 16 16" width="16" height="16">
              <path d="M3 8.5l3.2 3L13 4.5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Choice({ option }: { option: SelectOption }) {
  return (
    <span className="dd-choice">
      {option.icon && <span className="dd-icon">{option.icon}</span>}
      <span className="dd-text">
        <span className="dd-label">{option.label}</span>
        {option.hint && <span className="dd-hint">{option.hint}</span>}
      </span>
    </span>
  );
}
