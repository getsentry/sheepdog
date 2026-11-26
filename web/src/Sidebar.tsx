import { useEffect, useState } from 'react'
import { useAppState, useStore } from './context'
import { dangerTone, elapsed, relativeTime } from './format'
import { isOutdated, key, laneSections, shownClassification, type LaneSection } from './logic'
import { currentRepo, isMerging } from './store'
import type { Row } from './types'
import { Capsule, GearIcon, Modal, SearchList, Spinner, StopIcon, useNow } from './ui'

export function Sidebar({
  onApproveAndMerge,
  onOpenSettings,
}: {
  onApproveAndMerge: (rows: Row[]) => void
  onOpenSettings: () => void
}) {
  const state = useAppState()
  const { actions } = useStore()
  const repo = currentRepo(state)

  useEffect(() => {
    void actions.loadRepositories()
  }, [actions])

  return (
    <aside className="sidebar">
      <header>
        <RepoPicker />
      </header>
      <div className="sidebar-content">
        {state.requests === null && state.settings === null ? (
          <>
            <Banner />
            <div className="center">
              <Spinner />
            </div>
          </>
        ) : repo === '' ? (
          <>
            <Banner />
            <p className="placeholder">Select a repository</p>
          </>
        ) : (
          <RequestList onApproveAndMerge={onApproveAndMerge} />
        )}
      </div>
      <footer>
        {state.tasks.length > 0 && <TaskPanel />}
        <button type="button" className="borderless" onClick={onOpenSettings}>
          <GearIcon /> Settings
        </button>
      </footer>
    </aside>
  )
}

function RepoPicker() {
  const state = useAppState()
  const { actions } = useStore()
  const [open, setOpen] = useState(false)
  const repo = currentRepo(state)
  // The server keeps the name in lower case. The repository list has the name as GitHub shows it.
  const name = state.repositories?.find((item) => item.toLowerCase() === repo.toLowerCase()) ?? repo

  return (
    <>
      <button
        type="button"
        className="repo-picker borderless"
        onClick={() => {
          setOpen(true)
          void actions.loadRepositories()
        }}
      >
        {repo === '' ? 'Select a repository' : name} <span aria-hidden="true">▾</span>
      </button>
      {open && (
        <Modal label="Repositories" className="picker" onClose={() => setOpen(false)}>
          <SearchList
            prompt="Search repositories"
            items={state.repositories}
            error={state.repositoriesError}
            onSelect={(item) => {
              setOpen(false)
              if (item.toLowerCase() !== repo.toLowerCase()) void actions.selectRepo(item)
            }}
          />
        </Modal>
      )}
    </>
  )
}

function Banner() {
  const state = useAppState()
  const error = state.connectionError ?? state.error ?? state.requests?.error ?? state.requests?.rulesError
  if (!error) return null
  return (
    <p className="banner" role="alert">
      {error}
    </p>
  )
}

function RequestList({ onApproveAndMerge }: { onApproveAndMerge: (rows: Row[]) => void }) {
  const state = useAppState()
  const { actions } = useStore()
  const now = useNow(30000)
  const requests = state.requests
  const repo = currentRepo(state)
  const fetchedAt = requests?.fetchedAt

  function rowView(row: Row) {
    const selected = row.number === state.selection
    return (
      <li key={row.number}>
        <RowView
          row={row}
          repo={repo}
          now={now}
          selected={selected}
          onSelect={() => actions.select(row.number)}
        />
      </li>
    )
  }

  function header(section: LaneSection) {
    return (
      <h2 className="section-header">
        <span>{section.name}</span>
        <span className="muted number">{section.rows.length}</span>
        <span className="spacer" />
        {section.mergeable.length > 0 && (
          <button
            type="button"
            className="small"
            disabled={section.mergeable.some((row) => isMerging(state, repo, row.number))}
            onClick={() => onApproveAndMerge(section.mergeable)}
          >
            Approve and merge ({section.mergeable.length})
          </button>
        )}
      </h2>
    )
  }

  const sections = requests ? laneSections(requests) : null
  return (
    <>
      <Banner />
      {fetchedAt && (
        <p className="updated muted">
          Updated at {new Date(fetchedAt).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })}
        </p>
      )}
      {!fetchedAt && state.error === null && !requests?.error ? (
        <div className="center">
          <Spinner />
        </div>
      ) : !requests || requests.rows.length === 0 ? (
        <p className="placeholder">No review requests</p>
      ) : sections ? (
        sections.map((section) => (
          <section key={section.name} aria-label={section.name}>
            {header(section)}
            <ul className="rows">{section.rows.map(rowView)}</ul>
          </section>
        ))
      ) : (
        <ul className="rows" aria-label="Pull requests">
          {requests.rows.map(rowView)}
        </ul>
      )}
    </>
  )
}

function RowView({
  row,
  repo,
  now,
  selected,
  onSelect,
}: {
  row: Row
  repo: string
  now: number
  selected: boolean
  onSelect: () => void
}) {
  const state = useAppState()
  const shown = shownClassification(row, state.classifications[key(repo, row.number)])
  const outdated = shown !== null && isOutdated(shown, row.updatedAt)
  const score = shown?.value.score ?? 0
  const help =
    shown?.source === 'ci'
      ? `Danger ${score}/10 from CI`
      : outdated
        ? `Danger ${score}/10. The PR changed after this classification.`
        : `Danger ${score}/10`
  const notes = row.lane?.notes ?? []

  return (
    <button
      type="button"
      className="row"
      aria-current={selected || undefined}
      onClick={onSelect}
      onDoubleClick={() => window.open(row.url, '_blank', 'noopener,noreferrer')}
    >
      <span className="muted number">#{row.number}</span>
      <span className="row-main">
        <span className="row-title">
          {shown && (
            <span className={`score ${dangerTone(score)}${outdated ? ' outdated' : ''}`} title={help}>
              {score}
            </span>
          )}
          <span>{row.title}</span>
        </span>
        <span className="row-meta">
          <span className="muted">{row.author}</span>
          {row.isDraft && <Capsule tone="gray">Draft</Capsule>}
          {row.reasons.map((reason) => (
            <Capsule key={reason}>{reason}</Capsule>
          ))}
        </span>
        {notes.length > 0 && <span className="row-notes muted">{notes.join(' · ')}</span>}
      </span>
      <span className="row-side">
        <span className="number">
          <span className="text-green">+{row.additions}</span> <span className="text-red">−{row.deletions}</span>
        </span>
        <span className="muted" title={new Date(row.updatedAt).toLocaleString()}>
          {relativeTime(row.updatedAt, now)}
        </span>
      </span>
    </button>
  )
}

function TaskPanel() {
  const state = useAppState()
  const { actions } = useStore()
  const now = useNow(1000)
  return (
    <section className="tasks" aria-label="Running tasks">
      <h2 className="muted">Running tasks</h2>
      <ul>
        {state.tasks.map((task) => (
          <li key={task.id}>
            <Spinner label="Running" />
            <span className="task-text">
              <span className="ellipsis">{task.title}</span>
              <span className="muted ellipsis">
                {task.subtitle} · <span className="number">{elapsed(task.startedAt, now)}</span>
              </span>
            </span>
            <button
              type="button"
              className="borderless icon"
              title="Stop"
              aria-label={`Stop ${task.title}`}
              onClick={() => void actions.cancelTask(task.id)}
            >
              <StopIcon />
            </button>
          </li>
        ))}
      </ul>
    </section>
  )
}
