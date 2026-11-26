import { useEffect, useRef, useState, type CSSProperties } from 'react'
import { useAppState, useStore } from './context'
import { Detail } from './Detail'
import { key } from './logic'
import { Settings } from './Settings'
import { Sidebar } from './Sidebar'
import { SignIn } from './SignIn'
import { currentRepo, hasNoRules } from './store'
import type { Row } from './types'
import { Modal, Spinner } from './ui'

export function App() {
  const state = useAppState()
  if (state.auth === 'checking') {
    return (
      <div className="center full">
        <Spinner />
      </div>
    )
  }
  return state.auth === 'signedOut' ? <SignIn /> : <Main />
}

function Main() {
  const state = useAppState()
  const { actions } = useStore()
  // The rows that the user confirms, with the head commits that the dialog showed.
  const [batch, setBatch] = useState<Row[] | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [sidebarWidth, setSidebarWidth] = useState(storedSidebarWidth)
  const [resizing, setResizing] = useState(false)
  const repo = currentRepo(state)
  const row = state.requests?.rows.find((item) => item.number === state.selection) ?? null
  const title = row?.title

  useEffect(() => {
    document.title = title || 'Sheepdog'
    return () => {
      document.title = 'Sheepdog'
    }
  }, [title])

  return (
    <div
      className={resizing ? 'app resizing' : 'app'}
      style={{ '--sidebar-width': `${sidebarWidth}px` } as CSSProperties}
    >
      <Sidebar onApproveAndMerge={setBatch} onOpenSettings={() => setSettingsOpen(true)} />
      <SidebarResizer width={sidebarWidth} onChange={setSidebarWidth} onResizing={setResizing} />
      <main className="detail">
        {row ? (
          <Detail key={key(repo, row.number)} repo={repo} row={row} onApproveAndMerge={setBatch} />
        ) : (
          !hasNoRules(state.requests) && <p className="placeholder">Select a pull request</p>
        )}
      </main>
      {batch && (
        <MergeConfirmation
          rows={batch}
          onCancel={() => setBatch(null)}
          onConfirm={() => {
            setBatch(null)
            void actions.approveAndMerge(batch)
          }}
        />
      )}
      {settingsOpen && <Settings onClose={() => setSettingsOpen(false)} />}
    </div>
  )
}

const sidebarWidthKey = 'sheepdog.sidebarWidth'
const defaultSidebarWidth = 440
const minSidebarWidth = 300
// The detail panel keeps at least this width.
const minDetailWidth = 360

function clampSidebarWidth(width: number): number {
  return Math.round(Math.max(minSidebarWidth, Math.min(width, window.innerWidth - minDetailWidth)))
}

function storedSidebarWidth(): number {
  const stored = Number(localStorage.getItem(sidebarWidthKey))
  return stored > 0 ? clampSidebarWidth(stored) : defaultSidebarWidth
}

// The handle between the sidebar and the detail panel. It changes the width with the pointer or the arrow keys, and a double click
// gives the default width. The browser keeps the width, because it is a layout choice of this device.
function SidebarResizer({
  width,
  onChange,
  onResizing,
}: {
  width: number
  onChange: (width: number) => void
  onResizing: (on: boolean) => void
}) {
  const drag = useRef<{ x: number; width: number } | null>(null)

  function change(next: number, keep: boolean) {
    const clamped = clampSidebarWidth(next)
    onChange(clamped)
    if (keep) localStorage.setItem(sidebarWidthKey, String(clamped))
  }

  return (
    <div
      className="resizer"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the sidebar"
      aria-valuenow={width}
      aria-valuemin={minSidebarWidth}
      tabIndex={0}
      onPointerDown={(event) => {
        event.preventDefault()
        event.currentTarget.setPointerCapture(event.pointerId)
        drag.current = { x: event.clientX, width }
        onResizing(true)
      }}
      onPointerMove={(event) => {
        if (drag.current) change(drag.current.width + event.clientX - drag.current.x, false)
      }}
      onPointerUp={(event) => {
        if (!drag.current) return
        change(drag.current.width + event.clientX - drag.current.x, true)
        drag.current = null
        onResizing(false)
      }}
      onDoubleClick={() => change(defaultSidebarWidth, true)}
      onKeyDown={(event) => {
        const step = event.shiftKey ? 64 : 16
        if (event.key === 'ArrowLeft') change(width - step, true)
        else if (event.key === 'ArrowRight') change(width + step, true)
        else return
        event.preventDefault()
      }}
    />
  )
}

function MergeConfirmation({ rows, onCancel, onConfirm }: { rows: Row[]; onCancel: () => void; onConfirm: () => void }) {
  return (
    <Modal label="Approve and merge" className="confirm" onClose={onCancel}>
      <h2>
        {rows.length === 1 ? 'Approve and merge this pull request?' : `Approve and merge ${rows.length} pull requests?`}
      </h2>
      <p className="muted">
        Sheepdog approves each pull request with your GitHub account. Then it merges the pull request, or turns on
        auto-merge if GitHub cannot merge it yet.
      </p>
      <ul className="merge-list">
        {rows.map((item) => (
          <li key={item.number}>
            <span className="muted number">#{item.number}</span> {item.title}
          </li>
        ))}
      </ul>
      <div className="buttons">
        <span className="spacer" />
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className="primary" onClick={onConfirm}>
          Approve and merge
        </button>
      </div>
    </Modal>
  )
}
