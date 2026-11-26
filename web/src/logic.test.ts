import { describe, expect, it } from 'vitest'
import { checksText, checksTotal, isOutdated, key, laneSections, shownClassification, time } from './logic'
import { laneRow, requests } from './test/rows'
import { approveAndMergeAction, type CIClassification, type Classification } from './types'

describe('lanes', () => {
  it('keeps rows without lanes flat', () => {
    expect(laneSections(requests([laneRow(1, null), laneRow(2, null)]))).toBeNull()
    expect(laneSections(requests([], ['safe']))).toBeNull()
  })

  it('follows the file order, then manual', () => {
    const rows = [
      laneRow(1, 'manual'),
      laneRow(2, 'quick_look'),
      laneRow(3, 'safe', approveAndMergeAction),
      laneRow(4, null),
      laneRow(5, 'safe', approveAndMergeAction),
    ]
    const sections = laneSections(requests(rows, ['safe', 'docs', 'quick_look']))!
    expect(sections.map((s) => s.name)).toEqual(['safe', 'quick_look', 'manual'])
    expect(sections.map((s) => s.rows.map((r) => r.number))).toEqual([[3, 5], [2], [1, 4]])
    expect(sections.map((s) => s.mergeable.map((r) => r.number))).toEqual([[3, 5], [], []])
  })
})

const ciScore: CIClassification = { source: 'ci', score: 2, reason: 'Patch update.', model: 'repo/model', headSha: 'sha1' }

function stored(score: number, prUpdatedAt: string): Classification {
  return {
    repo: 'acme/app',
    number: 1,
    headSha: 'old',
    model: 'user/model',
    score,
    reason: 'Stored.',
    diffTruncated: false,
    prUpdatedAt,
    createdAt: prUpdatedAt,
  }
}

describe('shown classification', () => {
  it('puts the CI score before the app classification', () => {
    const row = laneRow(1, null, 'suggest', ciScore)
    const shown = shownClassification(row, stored(8, '2000-01-01T00:00:00Z'))!
    expect(shown).toEqual({ source: 'ci', value: ciScore })
    expect(shown.value.score).toBe(2)
    expect(shown.value.reason).toBe('Patch update.')
    expect(isOutdated(shown, row.updatedAt)).toBe(false)
  })

  it('uses the app classification without a CI score', () => {
    const row = laneRow(1, null)
    expect(shownClassification(row, undefined)).toBeNull()

    const old = stored(8, '2027-01-15T07:59:00Z')
    const outdated = shownClassification(row, old)!
    expect(outdated).toEqual({ source: 'app', value: old })
    expect(outdated.value.score).toBe(8)
    expect(isOutdated(outdated, row.updatedAt)).toBe(true)

    const current = shownClassification(row, stored(3, row.updatedAt))!
    expect(isOutdated(current, row.updatedAt)).toBe(false)
  })
})

describe('details', () => {
  it('lists the checks that are not zero', () => {
    expect(checksText({ passed: 23, failed: 10, pending: 0, skipped: 34 })).toBe('23 passed, 10 failed, 34 skipped')
    expect(checksTotal({ passed: 23, failed: 10, pending: 0, skipped: 34 })).toBe(67)
    expect(checksText({ passed: 4, failed: 0, pending: 0, skipped: 0 })).toBe('4 passed')
    expect(checksTotal({ passed: 0, failed: 0, pending: 0, skipped: 0 })).toBe(0)
  })

  it('ignores the case of repository names in keys', () => {
    expect(key('GetSentry/Sentry-React-Native', 6773)).toBe('getsentry/sentry-react-native#6773')
  })

  it('reads the times of Go with nine fraction digits and offsets', () => {
    expect(time('2026-11-16T11:24:55.569814123-05:30')).toBe(Date.UTC(2026, 10, 16, 16, 54, 55, 569))
    expect(time('2026-11-16T11:24:38.990425+01:00')).toBe(Date.UTC(2026, 10, 16, 10, 24, 38, 990))
    expect(time('2026-11-16T08:20:44Z')).toBe(Date.UTC(2026, 10, 16, 8, 20, 44))
  })
})
