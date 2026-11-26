import type { EventSourceLike } from '../events'

export interface Seen {
  method: string
  path: string
  body: unknown
  init: RequestInit
}

export interface Reply {
  status: number
  body?: unknown
}

export const reply = (status: number, body?: unknown): Reply => ({ status, body })
export const failure = (status: number, error: string): Reply => ({ status, body: { error } })

// A fetch that answers from the handler. A string body is sent as it is, any other body as JSON.
export function fakeFetch(handler: (seen: Seen) => Reply | Promise<Reply>) {
  const seen: Seen[] = []
  const fetcher: typeof fetch = async (input, init = {}) => {
    const request: Seen = {
      method: init.method ?? 'GET',
      path: String(input),
      body: typeof init.body === 'string' ? JSON.parse(init.body) : undefined,
      init,
    }
    seen.push(request)
    const { status, body } = await handler(request)
    const text = body === undefined ? null : typeof body === 'string' ? body : JSON.stringify(body)
    return new Response(status === 204 ? null : text, { status })
  }
  return { fetch: fetcher, seen }
}

// Holds a reply until the test opens it.
export class Gate {
  private release!: () => void
  private readonly opened = new Promise<void>((resolve) => (this.release = resolve))

  wait(): Promise<void> {
    return this.opened
  }

  open() {
    this.release()
  }
}

export class FakeSource implements EventSourceLike {
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  closed = false
  private readonly listeners = new Map<string, ((event: MessageEvent<string>) => void)[]>()

  constructor(readonly url: string) {}

  addEventListener(type: string, listener: (event: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  close() {
    this.closed = true
  }

  open() {
    this.onopen?.(new Event('open'))
  }

  // A string is sent as it is, any other value as JSON.
  emit(type: string, data: unknown) {
    const text = typeof data === 'string' ? data : JSON.stringify(data)
    for (const listener of this.listeners.get(type) ?? []) listener(new MessageEvent(type, { data: text }))
  }

  fail() {
    this.onerror?.(new Event('error'))
  }
}

// Each call gives a separate list of the opened sources.
export function fakeEventSources() {
  const sources: FakeSource[] = []
  class Source extends FakeSource {
    constructor(url: string) {
      super(url)
      sources.push(this)
    }
  }
  return { EventSource: Source, sources, last: () => sources[sources.length - 1] }
}

export function fakeTimers() {
  let next = 0
  const pending = new Map<number, () => void>()
  const delays: number[] = []
  return {
    delays,
    pending,
    setTimeout(callback: () => void, ms: number) {
      delays.push(ms)
      pending.set(++next, callback)
      return next
    },
    clearTimeout(timer: unknown) {
      pending.delete(timer as number)
    },
    // Runs the timers that wait now.
    fire() {
      const callbacks = [...pending.values()]
      pending.clear()
      callbacks.forEach((callback) => callback())
    },
  }
}

// Lets awaited promises and fetch replies finish.
export function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0))
}
