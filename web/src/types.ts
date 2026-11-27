// The JSON shapes of the Sheepdog server. Times are RFC 3339 text.

export interface Session {
  sessionToken: string
  login: string
}

export interface Settings {
  repo: string
  model: string
  hasOpenRouterKey: boolean
  // The starred repositories in lower case and in name order.
  starred: string[]
  // The repositories with auto classify or auto review on, in lower case and in name order.
  autoClassifyRepos: string[]
  autoReviewRepos: string[]
  // The rules for repositories without .github/sheepdog.yml, or "" without them.
  globalRules: string
}

// The settings of the user for one repository in lower case.
export interface RepoSettings {
  repo: string
  // Off when the server checks GitHub only on demand, not every 30 seconds.
  autoRefresh: boolean
  // On when the server classifies the PRs where only a score is missing for an approve-and-automerge lane.
  autoClassify: boolean
  // On when the server reviews each new head commit of the PRs that are not drafts.
  autoReview: boolean
}

export type Preference = 'autoRefresh' | 'autoClassify' | 'autoReview'

// A missing field stays unchanged. An empty openRouterKey deletes the key.
export interface SettingsUpdate {
  repo?: string
  model?: string
  openRouterKey?: string
  // An empty text deletes the global rules.
  globalRules?: string
}

export const manualLane = 'manual'
export const approveAndMergeAction = 'approve-and-automerge'

export interface Lane {
  name: string
  action: string
  notes: string[]
}

// The score of the Sheepdog workflow in GitHub Actions for the head commit of a row.
export interface CIClassification {
  source: 'ci'
  score: number
  reason: string
  model: string
  headSha: string
}

export interface Row {
  number: number
  title: string
  url: string
  isDraft: boolean
  updatedAt: string
  additions: number
  deletions: number
  author: string
  reasons: string[]
  headSha: string
  // Missing when the repository has no valid rules file.
  lane?: Lane
  // Missing without a CI score for the current head commit.
  classification?: CIClassification
  // True when review.comment of the rules lets the user post a review of the row to GitHub.
  postReviews?: boolean
}

// The result of the last poll. fetchedAt is null before the first successful poll.
export interface Requests {
  repo: string
  fetchedAt: string | null
  rows: Row[]
  // The lane names in the order of .github/sheepdog.yml, without manual.
  lanes: string[]
  // Missing before the first successful poll.
  defaultBranch?: string
  rulesError?: string
  error?: string
  // Set only when the default branch has no .github/sheepdog.yml. Without global rules, rows and lanes are then empty.
  setup?: Setup
  // True when the lanes come from the global rules of the user, because the default branch has no .github/sheepdog.yml.
  globalRules?: boolean
}

export interface PullLink {
  number: number
  url: string
}

export interface Setup {
  defaultBranch: string
  // The open PR from the branch sheepdog/setup to the default branch.
  pullRequest: PullLink | null
}

export interface Checks {
  passed: number
  failed: number
  pending: number
  skipped: number
}

export interface PullRequest {
  title: string
  body: string
  headRefName: string
  baseRefName: string
  headRefOid: string
  changedFiles: number
  createdAt: string
  updatedAt: string
  reviewDecision: string
  labels: string[]
  reviewRequests: string[]
  latestReviews: { author: string; state: string }[]
  checks: Checks
}

// truncated is true when the server cut a diff of more than 2 MB after its last full line.
export interface PullDiff {
  diff: string
  truncated: boolean
}

export interface Finding {
  severity: 'high' | 'medium' | 'low'
  file: string
  // The line number on side, or null for a finding about the whole file. Reviews from before line numbers have no side.
  line: number | null
  side?: 'new' | 'old'
  title: string
  detail: string
}

export interface Review {
  repo: string
  number: number
  headSha: string
  model: string
  risk: 'green' | 'yellow' | 'red'
  summary: string
  findings: Finding[]
  diffTruncated: boolean
  prUpdatedAt: string
  createdAt: string
  // The link of the GitHub review that the user posted from this review.
  postedUrl?: string
}

// A danger score from 1 (no danger, can be merged) to 10 (extreme danger).
export interface Classification {
  repo: string
  number: number
  headSha: string
  model: string
  score: number
  reason: string
  diffTruncated: boolean
  prUpdatedAt: string
  createdAt: string
}

export interface RunningTask {
  id: string
  kind: 'review' | 'classify'
  key: string
  title: string
  subtitle: string
  startedAt: string
}

// The end of a review task. A cancelled review sends no outcome.
export interface ReviewOutcome {
  repo: string
  number: number
  review?: Review
  error?: string
}

// The end of a classification task. A cancelled classification sends no outcome.
export interface ClassificationOutcome {
  repo: string
  number: number
  classification?: Classification
  error?: string
}

export interface MergeResult {
  result: 'merged' | 'auto-merge'
}

export type ServerEvent =
  | { type: 'requests'; data: Requests }
  | { type: 'tasks'; data: RunningTask[] }
  | { type: 'review'; data: ReviewOutcome }
  | { type: 'classification'; data: ClassificationOutcome }

export const eventTypes = ['requests', 'tasks', 'review', 'classification'] as const
