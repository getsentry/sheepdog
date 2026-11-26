import { time } from './logic'
import type { Checks, Finding, Review } from './types'

export type Tone = 'green' | 'yellow' | 'orange' | 'red' | 'gray'

export function dangerTone(score: number): Tone {
  return score <= 3 ? 'green' : score <= 6 ? 'orange' : 'red'
}

export function dangerLabel(score: number): string {
  if (score <= 1) return 'No danger, can be merged'
  if (score <= 3) return 'Low danger'
  if (score <= 6) return 'Medium danger'
  if (score <= 9) return 'High danger'
  return 'Extreme danger'
}

export const riskLabels: Record<Review['risk'], string> = {
  green: 'Low risk',
  yellow: 'Needs a careful review',
  red: 'High risk, review in full',
}

export const severityTones: Record<Finding['severity'], Tone> = { high: 'red', medium: 'orange', low: 'gray' }

export function checksTone(checks: Checks): Tone {
  return checks.failed > 0 ? 'red' : checks.pending > 0 ? 'orange' : 'green'
}

// "CHANGES_REQUESTED" becomes "Changes requested".
export function humanize(value: string): string {
  const words = value.toLowerCase().replaceAll('_', ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 31536000],
  ['month', 2592000],
  ['week', 604800],
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
]
const relative = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })

// now is in milliseconds.
export function relativeTime(date: string, now: number): string {
  const seconds = Math.round((time(date) - now) / 1000)
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return relative.format(Math.trunc(seconds / size), unit)
  }
  return 'just now'
}

// The running time of a task, as "1:05" or "1:02:05".
export function elapsed(startedAt: string, now: number): string {
  const total = Math.max(0, Math.floor((now - time(startedAt)) / 1000))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = String(total % 60).padStart(2, '0')
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, '0')}:${seconds}` : `${minutes}:${seconds}`
}

// PR templates hide their instructions in HTML comments.
export function bodyText(body: string): string {
  return body.replace(/<!--[\s\S]*?-->/g, '').trim()
}
