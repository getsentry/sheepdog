import { describe, expect, it, vi } from 'vitest'
import { ApiError } from './api'
import { openEventStream, reconnectDelay, type StreamHandlers } from './events'
import { fakeEventSources, fakeTimers, settle } from './test/fakes'

const task = {
  id: 'T1',
  kind: 'review',
  key: 'acme/app#1',
  title: 'Review of #1',
  subtitle: 'acme/app',
  startedAt: '2026-11-16T10:00:00Z',
}

// Opens a stream whose status checks give the replies in order.
function stream(statuses: (ApiError | null)[]) {
  const sources = fakeEventSources()
  const timers = fakeTimers()
  const updates: unknown[] = []
  const status = vi.fn(async () => statuses.shift() ?? null)
  const handlers: StreamHandlers = {
    connected: () => updates.push('connected'),
    event: (event) => updates.push(event),
    disconnected: (message) => updates.push(`disconnected: ${message}`),
    ended: (error) => updates.push(`ended: ${error.message}`),
  }
  const opened = openEventStream(handlers, { EventSource: sources.EventSource, status, ...timers })
  // Fails the current source, and waits for the status check and the delay.
  async function drop() {
    sources.last().fail()
    await settle()
    timers.fire()
  }
  return { ...sources, timers, updates, status, close: opened.close, drop }
}

describe('event stream', () => {
  it('waits 2, 4, 8, then 30 seconds', () => {
    expect([0, 1, 2, 3, 4, 5].map(reconnectDelay)).toEqual([2000, 4000, 8000, 30000, 30000, 30000])
  })

  it('connects again after drops, resets the delays after an event, and stops on 401', async () => {
    const s = stream([
      null,
      new ApiError(500, 'Internal server error'),
      new ApiError(500, 'The server returned HTTP 500'),
      new ApiError(502, 'The server returned HTTP 502'),
      new ApiError(0, 'The server is not reachable'),
      null,
      new ApiError(401, 'The session is not valid. Sign in again'),
    ])
    s.last().open()
    s.last().emit('tasks', [])
    for (let i = 0; i < 5; i++) await s.drop()
    s.last().open()
    s.last().emit('tasks', [task])
    s.last().emit('unknown', {})
    await s.drop()
    s.last().fail()
    await settle()

    expect(s.updates).toEqual([
      'connected',
      { type: 'tasks', data: [] },
      'disconnected: Internal server error',
      'disconnected: The server returned HTTP 500',
      'disconnected: The server returned HTTP 502',
      'disconnected: The server is not reachable',
      'connected',
      { type: 'tasks', data: [task] },
      'ended: The session is not valid. Sign in again',
    ])
    expect(s.timers.delays).toEqual([2000, 4000, 8000, 30000, 30000, 2000])
    expect(s.sources).toHaveLength(7)
    expect(s.sources.every((source) => source.closed && source.url === '/api/events')).toBe(true)
    expect(s.timers.pending.size).toBe(0)
  })

  it('skips events that are not JSON, waits 30 seconds after 429, and stops on 403', async () => {
    const s = stream([
      null,
      new ApiError(429, 'Too many open event streams. Close another client and try again'),
      null,
      new ApiError(403, 'This GitHub account is not allowed on this server'),
    ])
    s.last().open()
    s.last().emit('tasks', '[{"id":')
    s.last().emit('tasks', [task])
    await s.drop()
    await s.drop()
    s.last().open()
    s.last().emit('tasks', [])
    await s.drop()
    s.last().fail()
    await settle()

    expect(s.updates).toEqual([
      'connected',
      { type: 'tasks', data: [task] },
      'disconnected: Too many open event streams. Close another client and try again',
      'connected',
      { type: 'tasks', data: [] },
      'ended: This GitHub account is not allowed on this server',
    ])
    expect(s.timers.delays).toEqual([2000, 30000, 2000])
    expect(s.sources).toHaveLength(4)
  })

  it('does not connect again after close', async () => {
    const waiting = stream([null])
    waiting.last().fail()
    await settle()
    expect(waiting.timers.pending.size).toBe(1)
    waiting.close()
    waiting.timers.fire()
    expect(waiting.sources).toHaveLength(1)

    const checking = stream([new ApiError(401, 'Sign in first')])
    checking.last().fail()
    checking.close()
    await settle()
    expect(checking.updates).toEqual([])
    expect(checking.timers.delays).toEqual([])

    const open = stream([])
    open.close()
    expect(open.last().closed).toBe(true)
    open.last().open()
    open.last().emit('tasks', [])
    expect(open.updates).toEqual([])
    expect(open.status).not.toHaveBeenCalled()
  })
})
