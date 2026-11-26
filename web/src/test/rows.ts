import type { CIClassification, Requests, Row } from '../types'

export function laneRow(number: number, lane: string | null, action = 'suggest', classification?: CIClassification): Row {
  return {
    number,
    title: `PR ${number}`,
    url: `https://github.com/acme/app/pull/${number}`,
    isDraft: false,
    updatedAt: '2027-01-15T08:00:00Z',
    additions: 1,
    deletions: 1,
    author: 'dependabot',
    reasons: ['you'],
    headSha: `sha${number}`,
    ...(lane === null ? {} : { lane: { name: lane, action, notes: [] } }),
    ...(classification ? { classification } : {}),
  }
}

export function requests(rows: Row[], lanes: string[] = [], repo = 'acme/app'): Requests {
  return { repo, fetchedAt: null, rows, lanes }
}
