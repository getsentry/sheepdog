import { Fragment, useEffect, useState, type ReactNode } from 'react'
import Markdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useAppState, useStore } from './context'
import { findLine, parseDiff, samePath, type DiffFile } from './diff'
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
import type { Finding, PullRequest, Review, Row } from './types'
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

  const [diff, setDiff] = useState<{ files: DiffFile[]; truncated: boolean } | null>(null)
  const [diffError, setDiffError] = useState<string | null>(null)

  // A large diff loads apart from the PR, so that it does not hold back the details.
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const fetched = await actions.pullDiff(repo, row.number)
        if (cancelled || !fetched) return
        setDiff({ files: parseDiff(fetched.diff), truncated: fetched.truncated })
        setDiffError(null)
      } catch (failure) {
        if (!cancelled) setDiffError(failure instanceof Error ? failure.message : String(failure))
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
            <Changes
              diff={diff}
              error={diffError}
              url={row.url}
              findings={review && review.headSha === row.headSha ? review.findings : []}
            />
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

// A file with more changed lines starts collapsed, and its lines are not in the page until the user opens it.
const collapseAbove = 400

const statusLabels: Record<DiffFile['status'], string> = { added: 'Added', deleted: 'Deleted', renamed: 'Renamed', modified: '' }

const signs = { add: '+', delete: '-', context: ' ', note: '' } as const

// findings are the findings of a review of the current head commit, because the lines of an older commit can differ.
function Changes({
  diff,
  error,
  url,
  findings,
}: {
  diff: { files: DiffFile[]; truncated: boolean } | null
  error: string | null
  url: string
  findings: Finding[]
}) {
  const additions = diff?.files.reduce((sum, file) => sum + file.additions, 0) ?? 0
  const deletions = diff?.files.reduce((sum, file) => sum + file.deletions, 0) ?? 0
  return (
    <section className="changes" aria-label="Changes">
      <h2>
        Changes
        {diff && (
          <span className="muted number">
            {diff.files.length === 1 ? '1 file' : `${diff.files.length} files`}{' '}
            <span className="text-green">+{additions}</span> <span className="text-red">−{deletions}</span>
          </span>
        )}
      </h2>
      {error && <p className="error">{error}</p>}
      {diff === null && error === null && (
        <div className="center">
          <Spinner label="Loading the diff" />
        </div>
      )}
      {diff?.truncated && (
        <p className="note">
          The diff is larger than 2 MB, so Sheepdog shows only its first part.{' '}
          <ExternalLink href={`${url}/files`}>See all changes on GitHub</ExternalLink>
        </p>
      )}
      {diff?.files.length === 0 && <p className="muted">No changes</p>}
      {diff?.files.map((file) => (
        <DiffFileView
          key={`${file.oldPath}\n${file.path}`}
          file={file}
          findings={findings.filter((finding) => samePath(finding.file, file))}
        />
      ))}
    </section>
  )
}

// A finding shows under its line. A finding about the whole file, or with a line that the diff does not have, shows above the lines.
function DiffFileView({ file, findings }: { file: DiffFile; findings: Finding[] }) {
  const [open, setOpen] = useState(findings.length > 0 || file.additions + file.deletions <= collapseAbove)
  const status = file.binary ? 'Binary' : statusLabels[file.status]
  const atLine = new Map<string, Finding[]>()
  const aboveLines: Finding[] = []
  for (const finding of findings) {
    const place = finding.line === null ? null : findLine(file, finding.side ?? 'new', finding.line)
    if (!place) {
      aboveLines.push(finding)
      continue
    }
    const k = `${place.hunk}:${place.index}`
    atLine.set(k, [...(atLine.get(k) ?? []), finding])
  }
  return (
    <section className="diff-file" aria-label={file.path}>
      <button type="button" className="diff-file-header" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span aria-hidden="true">{open ? '▾' : '▸'}</span>
        <span className="diff-path">{file.status === 'renamed' ? `${file.oldPath} → ${file.path}` : file.path}</span>
        {status && <Capsule tone="gray">{status}</Capsule>}
        {findings.length > 0 && <Capsule>{findings.length === 1 ? '1 finding' : `${findings.length} findings`}</Capsule>}
        <span className="spacer" />
        <span className="number">
          <span className="text-green">+{file.additions}</span> <span className="text-red">−{file.deletions}</span>
        </span>
      </button>
      {open && aboveLines.length > 0 && (
        <div className="diff-findings">
          {aboveLines.map((finding, index) => (
            <FindingView key={index} finding={finding} />
          ))}
        </div>
      )}
      {open &&
        (file.hunks.length === 0 ? (
          <p className="muted diff-note">{file.binary ? 'Binary file' : 'No changed lines'}</p>
        ) : (
          <div className="diff-lines">
            <table>
              <tbody>
                {file.hunks.map((hunk, index) => (
                  <Fragment key={index}>
                    <tr className="hunk">
                      <td colSpan={3}>{hunk.header}</td>
                    </tr>
                    {hunk.lines.map((line, number) => (
                      <Fragment key={number}>
                        <tr className={line.kind}>
                          <td className="line-number">{line.oldNumber}</td>
                          <td className="line-number">{line.newNumber}</td>
                          <td className="code">
                            {signs[line.kind]}
                            {line.text}
                          </td>
                        </tr>
                        {atLine.get(`${index}:${number}`)?.map((finding, at) => (
                          <tr key={at} className="comment">
                            <td colSpan={3}>
                              <FindingView finding={finding} inline />
                            </td>
                          </tr>
                        ))}
                      </Fragment>
                    ))}
                  </Fragment>
                ))}
              </tbody>
            </table>
          </div>
        ))}
    </section>
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
        <FindingView key={index} finding={finding} />
      ))}
    </section>
  )
}

// inline is a finding under its line in the diff, which needs no file and line.
function FindingView({ finding, inline = false }: { finding: Finding; inline?: boolean }) {
  return (
    <div className={inline ? `finding inline ${finding.severity}` : 'finding'}>
      <div className="finding-title">
        <Capsule tone={severityTones[finding.severity]}>{humanize(finding.severity)}</Capsule>
        <strong>{finding.title}</strong>
      </div>
      {!inline && (
        <code className="muted">
          {finding.file}
          {finding.line !== null && `:${finding.line}`}
        </code>
      )}
      <p className="plain">{finding.detail}</p>
    </div>
  )
}
