import { useEffect, useState, type ReactNode } from 'react'
import Markdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useAppState, useStore } from './context'
import { bodyText, checksTone, dangerLabel, dangerTone, humanize, riskLabels, severityTones } from './format'
import {
  canApproveAndMerge,
  checksText,
  checksTotal,
  isOutdated,
  key,
  shownClassification,
  time,
  type ShownClassification,
} from './logic'
import { isClassifying, isMerging, isReviewing } from './store'
import type { PullRequest, Review, Row } from './types'
import { Capsule, ExternalLink, Spinner } from './ui'

const truncatedNote = 'The diff was too large. The model saw only the first 200,000 characters.'

const shortTime = (text: string) => new Date(text).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })

// The PR body comes from GitHub users. Raw HTML stays text, and the CSP blocks images of other hosts, so an image is a link.
const markdownComponents: Components = {
  a: ({ node: _, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer" />,
  img: ({ src, alt }) =>
    typeof src === 'string' && src !== '' ? <ExternalLink href={src}>{alt || src}</ExternalLink> : <>{alt}</>,
}

export function Detail({
  repo,
  row,
  onApproveAndMerge,
}: {
  repo: string
  row: Row
  onApproveAndMerge: (rows: Row[]) => void
}) {
  const state = useAppState()
  const { actions } = useStore()
  const [detail, setDetail] = useState<PullRequest | null>(null)
  const [error, setError] = useState<string | null>(null)
  const k = key(repo, row.number)
  const review = state.reviews[k]
  const reviewError = state.reviewErrors[k]
  const classification = state.classifications[k]
  const classificationError = state.classificationErrors[k]
  const classifying = isClassifying(state, repo, row.number)
  const reviewing = isReviewing(state, repo, row.number)
  const merging = isMerging(state, repo, row.number)
  const shown = shownClassification(row, classification)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const [fetched] = await Promise.all([
          actions.pullRequest(repo, row.number),
          actions.loadReview(repo, row.number),
          actions.loadClassification(repo, row.number),
        ])
        if (cancelled || !fetched) return
        setDetail(fetched)
        setError(null)
      } catch (failure) {
        if (!cancelled) setError(failure instanceof Error ? failure.message : String(failure))
      }
    })()
    return () => {
      cancelled = true
    }
  }, [actions, repo, row.number, row.updatedAt])

  const body = detail ? bodyText(detail.body) : ''

  return (
    <>
      <header className="detail-header">
        <span className="number">#{row.number}</span>
        <span>{row.author}</span>
        {row.isDraft && <Capsule tone="gray">Draft</Capsule>}
        <span className="spacer" />
        <button
          type="button"
          disabled={detail === null || classifying}
          onClick={() => void actions.startClassification(repo, row.number)}
        >
          {classifying ? (
            <>
              <Spinner label="Classifying" /> Classifying…
            </>
          ) : classification ? (
            'Classify again'
          ) : (
            'Classify'
          )}
        </button>
        <button
          type="button"
          disabled={detail === null || reviewing}
          onClick={() => void actions.startReview(repo, row.number)}
        >
          {reviewing ? (
            <>
              <Spinner label="Reviewing" /> Reviewing…
            </>
          ) : review ? (
            'Review again'
          ) : (
            'Review'
          )}
        </button>
        {canApproveAndMerge(row) && (
          <button type="button" disabled={merging} onClick={() => onApproveAndMerge([row])}>
            {merging ? (
              <>
                <Spinner label="Merging" /> Merging…
              </>
            ) : (
              'Approve and merge'
            )}
          </button>
        )}
        <ExternalLink href={row.url} className="button">
          Open on GitHub
        </ExternalLink>
      </header>
      <article className="detail-content">
        <h1>{row.title}</h1>
        {detail && (
          <p className="branch muted">
            {detail.baseRefName} ← {detail.headRefName}
          </p>
        )}
        {error && <p className="error">{error}</p>}
        {detail ? (
          <>
            <Facts row={row} detail={detail} />
            {classificationError && <p className="error">{classificationError}</p>}
            {shown && <ClassificationSection shown={shown} prUpdatedAt={row.updatedAt} />}
            {reviewError && <p className="error">{reviewError}</p>}
            {review && <ReviewSection review={review} prUpdatedAt={row.updatedAt} />}
            <hr />
            {body === '' ? (
              <p className="muted">No description</p>
            ) : (
              <div className="markdown">
                <Markdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
                  {body}
                </Markdown>
              </div>
            )}
          </>
        ) : (
          error === null && (
            <div className="center">
              <Spinner />
            </div>
          )
        )}
      </article>
    </>
  )
}

function Fact({ title, children }: { title: string; children: ReactNode }) {
  return (
    <>
      <dt>{title}</dt>
      <dd>{children}</dd>
    </>
  )
}

function Facts({ row, detail }: { row: Row; detail: PullRequest }) {
  const checks = detail.checks
  return (
    <dl className="facts">
      {row.lane && (
        <Fact title="Lane">
          <div>{row.lane.name}</div>
          {row.lane.notes.map((note) => (
            <div key={note} className="muted">
              {note}
            </div>
          ))}
        </Fact>
      )}
      <Fact title="Created">{new Date(detail.createdAt).toLocaleString()}</Fact>
      <Fact title="Changes">
        <span className="number">
          <span className="text-green">+{row.additions}</span> <span className="text-red">−{row.deletions}</span> in{' '}
          {detail.changedFiles} files
        </span>
      </Fact>
      <Fact title="Review">{humanize(detail.reviewDecision || 'NONE')}</Fact>
      <Fact title="Checks">
        {checksTotal(checks) === 0 ? 'None' : <span className={`text-${checksTone(checks)}`}>{checksText(checks)}</span>}
      </Fact>
      <Fact title="Requested">{detail.reviewRequests.join(', ')}</Fact>
      {detail.latestReviews.length > 0 && (
        <Fact title="Reviews">
          {detail.latestReviews.map((review) => (
            <div key={review.author}>
              {review.author}: {humanize(review.state)}
            </div>
          ))}
        </Fact>
      )}
      {detail.labels.length > 0 && (
        <Fact title="Labels">
          <span className="capsules">
            {detail.labels.map((label) => (
              <Capsule key={label}>{label}</Capsule>
            ))}
          </span>
        </Fact>
      )}
    </dl>
  )
}

// Model text is plain text, never Markdown.
function ClassificationSection({ shown, prUpdatedAt }: { shown: ShownClassification; prUpdatedAt: string }) {
  const { score, model, reason } = shown.value
  const tone = dangerTone(score)
  return (
    <section className={`card ${tone}`} aria-label="Classification">
      <span className={`big-score text-${tone}`}>{score}/10</span>
      <div className="card-main">
        <div className="card-header">
          <strong>{dangerLabel(score)}</strong>
          <span className="spacer" />
          <span className="muted small-text">
            {shown.source === 'ci' ? `From CI, ${model}` : `${model}, ${shortTime(shown.value.createdAt)}`}
          </span>
        </div>
        {isOutdated(shown, prUpdatedAt) && (
          <p className="note">The PR changed after this classification.</p>
        )}
        {shown.source === 'app' && shown.value.diffTruncated && <p className="note">{truncatedNote}</p>}
        <p className="plain">{reason}</p>
      </div>
    </section>
  )
}

function ReviewSection({ review, prUpdatedAt }: { review: Review; prUpdatedAt: string }) {
  return (
    <section className={`card column ${review.risk}`} aria-label="Review">
      <div className="card-header">
        <span className={`dot ${review.risk}`} />
        <strong>{riskLabels[review.risk]}</strong>
        <span className="spacer" />
        <span className="muted small-text">
          {review.model}, {shortTime(review.createdAt)}
        </span>
      </div>
      {time(prUpdatedAt) > time(review.prUpdatedAt) && <p className="note">The PR changed after this review.</p>}
      {review.diffTruncated && <p className="note">{truncatedNote}</p>}
      <p className="plain">{review.summary}</p>
      {review.findings.map((finding, index) => (
        <div key={index} className="finding">
          <div className="finding-title">
            <Capsule tone={severityTones[finding.severity]}>{humanize(finding.severity)}</Capsule>
            <strong>{finding.title}</strong>
          </div>
          <code className="muted">{finding.file}</code>
          <p className="plain">{finding.detail}</p>
        </div>
      ))}
    </section>
  )
}
