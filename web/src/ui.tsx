import { useEffect, useRef, useState, type ReactNode } from 'react'
import type { Tone } from './format'

// The current time, updated every interval milliseconds.
export function useNow(interval: number): number {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), interval)
    return () => clearInterval(timer)
  }, [interval])
  return now
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return <span className="spinner" role="status" aria-label={label} />
}

export function Capsule({ tone, children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`capsule ${tone ?? 'accent'}`}>{children}</span>
}

export function ExternalLink({ href, className, children }: { href: string; className?: string; children: ReactNode }) {
  return (
    <a href={href} className={className} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  )
}

// A modal <dialog> that is open while it is mounted. Escape and a click on the backdrop call onClose.
export function Modal({
  label,
  className,
  onClose,
  children,
}: {
  label: string
  className?: string
  onClose: () => void
  children: ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = ref.current!
    if (!dialog.open) dialog.showModal()
  }, [])
  // React passes close and click events of a nested dialog to the handlers of this dialog too.
  return (
    <dialog
      ref={ref}
      aria-label={label}
      className={className}
      onClose={(event) => event.target === event.currentTarget && onClose()}
      onClick={(event) => event.target === event.currentTarget && event.currentTarget.close()}
    >
      <div className="dialog-body">{children}</div>
    </dialog>
  )
}

// A search field over a list. items is null while the list loads. action adds a control after each item.
export function SearchList({
  prompt,
  items,
  error,
  onSelect,
  action,
}: {
  prompt: string
  items: string[] | null
  error: string | null
  onSelect: (item: string) => void
  action?: (item: string) => ReactNode
}) {
  const [search, setSearch] = useState('')
  const matches = (items ?? []).filter((item) => item.toLowerCase().includes(search.toLowerCase()))
  return (
    <div className="search-list">
      <input
        type="search"
        aria-label={prompt}
        placeholder={prompt}
        value={search}
        autoFocus
        onChange={(event) => setSearch(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && matches.length > 0) onSelect(matches[0])
        }}
      />
      {error ? (
        <p className="error">{error}</p>
      ) : items === null ? (
        <div className="center">
          <Spinner />
        </div>
      ) : (
        <ul aria-label={prompt}>
          {matches.map((item) => (
            <li key={item}>
              <button type="button" onClick={() => onSelect(item)}>
                {item}
              </button>
              {action?.(item)}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

export function Switch({
  label,
  checked,
  disabled,
  onChange,
}: {
  label: string
  checked: boolean
  disabled?: boolean
  onChange?: (on: boolean) => void
}) {
  return (
    <label className="switch">
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange?.(event.target.checked)}
      />
      {label}
    </label>
  )
}

export function GearIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path
        fill="currentColor"
        d="M19.4 13a7.5 7.5 0 0 0 0-2l2.1-1.6-2-3.5-2.5 1a7.6 7.6 0 0 0-1.7-1L15 3.3h-4l-.4 2.6a7.6 7.6 0 0 0-1.7 1l-2.5-1-2 3.5L6.6 11a7.5 7.5 0 0 0 0 2l-2.1 1.6 2 3.5 2.5-1a7.6 7.6 0 0 0 1.7 1l.4 2.6h4l.4-2.6a7.6 7.6 0 0 0 1.7-1l2.5 1 2-3.5ZM12 15.5a3.5 3.5 0 1 1 0-7 3.5 3.5 0 0 1 0 7Z"
      />
    </svg>
  )
}

export function StarIcon({ filled }: { filled: boolean }) {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path
        fill={filled ? 'currentColor' : 'none'}
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
        d="m12 3.2 2.7 5.5 6 .9-4.3 4.2 1 6-5.4-2.8-5.4 2.8 1-6-4.3-4.2 6-.9Z"
      />
    </svg>
  )
}

export function SlidersIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <g fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
        <path d="M4 7h9M17 7h3M4 17h3M11 17h9" />
        <circle cx="15" cy="7" r="2" />
        <circle cx="9" cy="17" r="2" />
      </g>
    </svg>
  )
}

export function RefreshIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3M19.5 4v4.5H15"
      />
    </svg>
  )
}

export function StopIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="2" />
      <rect x="9" y="9" width="6" height="6" fill="currentColor" />
    </svg>
  )
}
