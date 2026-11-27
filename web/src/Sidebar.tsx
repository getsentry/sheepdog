import { useEffect, useState } from 'react'
import { useAppState, useStore } from './context'
import { dangerTone, elapsed, relativeTime } from './format'
import { isOutdated, key, laneSections, pickerOrder, shownClassification, type LaneSection } from './logic'
import { RepoSettings } from './RepoSettings'
import { currentRepo, hasNoRules, isAutoClassified, isAutoReviewed, isMerging, isSettingUp, isStarred } from './store'
import type { PullLink, Row, Setup } from './types'
import {
  BubbleIcon,
  Capsule,
  CheckIcon,
  ExternalLink,
  GaugeIcon,
  GearIcon,
  Modal,
  RefreshIcon,
  SearchList,
  SlidersIcon,
  Spinner,
  StarIcon,
  StopIcon,
  useNow,
} from './ui'

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
        ) : hasNoRules(state.requests) && state.requests?.setup ? (
          <>
            <Banner />
            <SetupWarning repo={repo} setup={state.requests.setup} onOpenSettings={onOpenSettings} />
          </>
        ) : (
          <RequestList onApproveAndMerge={onApproveAndMerge} onOpenSettings={onOpenSettings} />
        )}
      </div>
      <footer>
        {state.tasks.length > 0 && <TaskPanel />}
        {repo !== '' && <RefreshBar repo={repo} />}
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
  const repositories =
    state.repositories &&
    pickerOrder(state.repositories, state.settings?.starred ?? [], [
      ...(state.settings?.autoClassifyRepos ?? []),
      ...(state.settings?.autoReviewRepos ?? []),
    ])

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
          {state.starError && (
            <p className="error" role="alert">
              {state.starError}
            </p>
          )}
          <SearchList
            prompt="Search repositories"
            items={repositories}
            error={state.repositoriesError}
            onSelect={(item) => {
              setOpen(false)
              if (item.toLowerCase() !== repo.toLowerCase()) void actions.selectRepo(item)
            }}
            action={(item) => {
              const starred = isStarred(state, item)
              const label = starred ? 'Remove star' : 'Star'
              return (
                <>
                  {isAutoClassified(state, item) && (
                    <span className="repo-flag" role="img" aria-label="Auto-classify is on" title="Auto-classify is on">
                      <GaugeIcon />
                    </span>
                  )}
                  {isAutoReviewed(state, item) && (
                    <span className="repo-flag" role="img" aria-label="Auto-review is on" title="Auto-review is on">
                      <BubbleIcon />
                    </span>
                  )}
                  <button
                    type="button"
                    className={`star borderless icon${starred ? ' starred' : ''}`}
                    title={label}
                    aria-label={label}
                    disabled={state.settings === null}
                    onClick={() => void actions.toggleStar(item)}
                  >
                    <StarIcon filled={starred} />
                  </button>
                </>
              )
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

// compact is the short warning above the list of a repository that uses the global rules.
function SetupWarning({
  repo,
  setup,
  compact = false,
  onOpenSettings,
}: {
  repo: string
  setup: Setup
  compact?: boolean
  onOpenSettings: () => void
}) {
  const [dialogOpen, setDialogOpen] = useState(false)
  const size = compact ? 'small' : ''
  return (
    <section className={compact ? 'setup compact' : 'setup'} aria-label="Setup">
      {!compact && <h2>No Sheepdog rules</h2>}
      <p>
        {repo} has no <code>.github/sheepdog.yml</code> on its default branch <code>{setup.defaultBranch}</code>.{' '}
        {compact
          ? 'Sheepdog uses your global rules for it, not rules of the repository.'
          : 'Sheepdog works only with repositories that have this file, or with your global rules.'}
      </p>
      {setup.pullRequest && (
        <p>
          Sheepdog waits for the merge of{' '}
          <ExternalLink href={setup.pullRequest.url}>pull request #{setup.pullRequest.number}</ExternalLink>, which adds
          the file.
        </p>
      )}
      <div className="buttons">
        {!setup.pullRequest && (
          <button type="button" className={compact ? size : 'primary'} onClick={() => setDialogOpen(true)}>
            Create sheepdog.yml
          </button>
        )}
        <button type="button" className={size} onClick={onOpenSettings}>
          {compact ? 'Edit global rules' : 'Set global rules'}
        </button>
      </div>
      {dialogOpen && <SetupDialog repo={repo} onClose={() => setDialogOpen(false)} />}
    </section>
  )
}

// Mounted only while open. It loads the template again each time, and sends nothing before "Open pull request".
function SetupDialog({ repo, onClose }: { repo: string; onClose: () => void }) {
  const state = useAppState()
  const { actions } = useStore()
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<PullLink | null>(null)
  const sending = isSettingUp(state, repo)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const template = await actions.setupTemplate(repo)
        if (!cancelled && template !== null) setContent(template)
      } catch (failure) {
        if (!cancelled) setError(failure instanceof Error ? failure.message : String(failure))
      }
    })()
    return () => {
      cancelled = true
    }
  }, [actions, repo])

  async function openPullRequest() {
    if (content === null) return
    setError(null)
    try {
      const link = await actions.setUp(repo, content)
      if (link) setCreated(link)
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
    }
  }

  return (
    <Modal label="Create sheepdog.yml" className="setup-dialog" onClose={onClose}>
      <h2>Create .github/sheepdog.yml</h2>
      {created ? (
        <>
          <p>
            Sheepdog opened <ExternalLink href={created.url}>pull request #{created.number}</ExternalLink>. After its
            merge, Sheepdog shows the review requests of {repo}.
          </p>
          <div className="buttons">
            <span className="spacer" />
            <button type="button" className="primary" onClick={onClose}>
              Close
            </button>
          </div>
        </>
      ) : (
        <>
          <p className="muted">
            Sheepdog opens a pull request from the branch <code>sheepdog/setup</code> that adds this file. Edit the
            rules before you open it.
          </p>
          {content === null ? (
            error === null && (
              <div className="center">
                <Spinner />
              </div>
            )
          ) : (
            <textarea
              aria-label="sheepdog.yml"
              spellCheck={false}
              value={content}
              disabled={sending}
              onChange={(event) => setContent(event.target.value)}
            />
          )}
          {error && <p className="error">{error}</p>}
          <div className="buttons">
            <span className="spacer" />
            <button type="button" onClick={onClose}>
              Cancel
            </button>
            <button
              type="button"
              className="primary"
              disabled={content === null || sending}
              onClick={() => void openPullRequest()}
            >
              {sending ? (
                <>
                  <Spinner label="Opening" /> Opening…
                </>
              ) : (
                'Open pull request'
              )}
            </button>
          </div>
        </>
      )}
    </Modal>
  )
}

function RequestList({
  onApproveAndMerge,
  onOpenSettings,
}: {
  onApproveAndMerge: (rows: Row[]) => void
  onOpenSettings: () => void
}) {
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
      {requests?.setup && requests.globalRules && (
        <SetupWarning repo={repo} setup={requests.setup} compact onOpenSettings={onOpenSettings} />
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
  const review = state.reviews[key(repo, row.number)]
  const reviewHelp = review?.headSha === row.headSha ? 'Reviewed' : 'Reviewed an older commit'

  return (
    <button
      type="button"
      className="row"
      aria-current={selected || undefined}
      onClick={onSelect}
      onDoubleClick={() => window.open(row.url, '_blank', 'noopener,noreferrer')}
    >
      <span className="row-lead">
        <span className="muted number">#{row.number}</span>
        {(shown || review) && (
          <span className="row-badges">
            {shown && (
              <span className={`score ${dangerTone(score)}${outdated ? ' outdated' : ''}`} title={help}>
                {score}
              </span>
            )}
            {review && (
              <span
                className={review.headSha === row.headSha ? 'reviewed' : 'reviewed outdated'}
                role="img"
                aria-label={reviewHelp}
                title={reviewHelp}
              >
                <CheckIcon />
              </span>
            )}
          </span>
        )}
      </span>
      <span className="row-main">
        <span className="row-title">
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

function RefreshBar({ repo }: { repo: string }) {
  const state = useAppState()
  const { actions } = useStore()
  const [settingsOpen, setSettingsOpen] = useState(false)
  const fetchedAt = state.requests?.fetchedAt
  return (
    <section className="refresh" aria-label="Updates">
      <div className="refresh-bar">
        <span className="muted">
          {fetchedAt
            ? `Updated at ${new Date(fetchedAt).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })}`
            : 'Not updated yet'}
        </span>
        <button
          type="button"
          className="borderless icon"
          title="Refresh"
          aria-label="Refresh"
          disabled={state.refreshing}
          onClick={() => void actions.refresh()}
        >
          {state.refreshing ? <Spinner label="Refreshing" /> : <RefreshIcon />}
        </button>
        <span className="spacer" />
        <button type="button" className="borderless small" onClick={() => setSettingsOpen(true)}>
          <SlidersIcon /> Repository settings
        </button>
      </div>
      {state.refreshError && (
        <p className="error" role="alert">
          {state.refreshError}
        </p>
      )}
      {settingsOpen && <RepoSettings repo={repo} onClose={() => setSettingsOpen(false)} />}
    </section>
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
