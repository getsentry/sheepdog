import { describe, expect, it } from 'vitest'
import { bodyText, checksTone, dangerLabel, dangerTone, elapsed, humanize, relativeTime } from './format'

const now = new Date('2027-01-15T12:00:00Z').getTime()

describe('format', () => {
  it('colors and names the danger score', () => {
    expect([1, 3, 4, 6, 7, 10].map(dangerTone)).toEqual(['green', 'green', 'orange', 'orange', 'red', 'red'])
    expect([1, 2, 5, 8, 10].map(dangerLabel)).toEqual([
      'No danger, can be merged',
      'Low danger',
      'Medium danger',
      'High danger',
      'Extreme danger',
    ])
  })

  it('colors the checks by the worst state', () => {
    expect(checksTone({ passed: 3, failed: 1, pending: 1, skipped: 0 })).toBe('red')
    expect(checksTone({ passed: 3, failed: 0, pending: 1, skipped: 0 })).toBe('orange')
    expect(checksTone({ passed: 3, failed: 0, pending: 0, skipped: 2 })).toBe('green')
  })

  it('humanizes the GitHub states', () => {
    expect(humanize('CHANGES_REQUESTED')).toBe('Changes requested')
    expect(humanize('NONE')).toBe('None')
  })

  it('gives the time since an update in the largest unit', () => {
    expect(relativeTime('2027-01-15T11:59:30Z', now)).toBe('just now')
    expect(relativeTime('2027-01-15T11:55:00Z', now)).toBe('5 minutes ago')
    expect(relativeTime('2027-01-15T09:00:00Z', now)).toBe('3 hours ago')
    expect(relativeTime('2027-01-14T11:00:00Z', now)).toBe('yesterday')
    expect(relativeTime('2026-12-01T12:00:00Z', now)).toBe('last month')
  })

  it('gives the running time of a task', () => {
    expect(elapsed('2027-01-15T11:59:55Z', now)).toBe('0:05')
    expect(elapsed('2027-01-15T11:58:55Z', now)).toBe('1:05')
    expect(elapsed('2027-01-15T10:57:55Z', now)).toBe('1:02:05')
    expect(elapsed('2027-01-15T12:00:05Z', now)).toBe('0:00')
  })

  it('removes the HTML comments of a PR body', () => {
    expect(bodyText('<!-- Describe\nthe change -->\n## Summary\nText <!-- x -->')).toBe('## Summary\nText')
  })
})
