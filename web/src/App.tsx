import { useEffect, useState } from 'react'
import { useAppState, useStore } from './context'
import { Detail } from './Detail'
import { key } from './logic'
import { Settings } from './Settings'
import { Sidebar } from './Sidebar'
import { SignIn } from './SignIn'
import { currentRepo } from './store'
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
    <div className="app">
      <Sidebar onApproveAndMerge={setBatch} onOpenSettings={() => setSettingsOpen(true)} />
      <main className="detail">
        {row ? (
          <Detail key={key(repo, row.number)} repo={repo} row={row} onApproveAndMerge={setBatch} />
        ) : (
          !state.requests?.setup && <p className="placeholder">Select a pull request</p>
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
