import {
  approveAndMergeAction,
  manualLane,
  type Checks,
  type CIClassification,
  type Classification,
  type Requests,
  type Row,
} from './types'

// The server sends repository names in lower case, and GitHub ignores their case.
export function key(repo: string, number: number): string {
  return `${repo.toLowerCase()}#${number}`
}

export function time(text: string): number {
  return new Date(text).getTime()
}

export function canApproveAndMerge(row: Row): boolean {
  return row.lane?.action === approveAndMergeAction
}

// The score that the list and the detail panel show for a row.
export type ShownClassification = { source: 'ci'; value: CIClassification } | { source: 'app'; value: Classification }

// The CI score of the head commit comes before the stored app classification.
export function shownClassification(row: Row, stored: Classification | undefined): ShownClassification | null {
  if (row.classification) return { source: 'ci', value: row.classification }
  if (stored) return { source: 'app', value: stored }
  return null
}

// A CI score is always for the current head commit.
export function isOutdated(shown: ShownClassification, prUpdatedAt: string): boolean {
  return shown.source === 'app' && time(prUpdatedAt) > time(shown.value.prUpdatedAt)
}

export interface LaneSection {
  name: string
  rows: Row[]
  mergeable: Row[]
}

// null when no row has a lane, so that the list stays flat. A row without a lane, from a base branch without rules, is in manual.
export function laneSections(requests: Requests): LaneSection[] | null {
  if (!requests.rows.some((row) => row.lane)) return null
  return [...requests.lanes, manualLane].flatMap((name) => {
    const rows = requests.rows.filter((row) => (row.lane?.name ?? manualLane) === name)
    return rows.length === 0 ? [] : [{ name, rows, mergeable: rows.filter(canApproveAndMerge) }]
  })
}

export function checksTotal(checks: Checks): number {
  return checks.passed + checks.failed + checks.pending + checks.skipped
}

export function checksText(checks: Checks): string {
  return (
    [
      [checks.passed, 'passed'],
      [checks.failed, 'failed'],
      [checks.pending, 'pending'],
      [checks.skipped, 'skipped'],
    ] as const
  )
    .filter(([count]) => count > 0)
    .map(([count, label]) => `${count} ${label}`)
    .join(', ')
}
