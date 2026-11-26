import type { ApiError } from './api'
import { eventTypes, type ServerEvent } from './types'

export interface StreamHandlers {
  connected(): void
  event(event: ServerEvent): void
  // The stream failed and connects again after a delay.
  disconnected(message: string): void
  // The server refused the session with 401 or 403. The stream does not connect again.
  ended(error: ApiError): void
}

export interface EventSourceLike {
  onopen: ((event: Event) => void) | null
  onerror: ((event: Event) => void) | null
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void
  close(): void
}

export interface StreamDeps {
  EventSource: new (url: string) => EventSourceLike
  // Reads the status that the server gives a new stream: null for a stream that the server accepts.
  status(): Promise<ApiError | null>
  setTimeout(callback: () => void, ms: number): unknown
  clearTimeout(timer: unknown): void
}

// 2, 4, and 8 seconds, then every 30 seconds.
export function reconnectDelay(attempt: number): number {
  return [2000, 4000, 8000][attempt] ?? 30000
}

// Too many open streams: a fast retry cannot succeed before another client closes its stream.
const tooManyStreamsDelay = 30000

// Opens /api/events, and connects again after each failure until close() or a refused session.
// EventSource connects again by itself without the status of the failure, so the stream closes it and decides here.
export function openEventStream(handlers: StreamHandlers, deps: StreamDeps): { close(): void } {
  let source: EventSourceLike | null = null
  let timer: unknown = null
  let attempt = 0
  let stopped = false

  function connect() {
    const current = new deps.EventSource('/api/events')
    source = current
    current.onopen = () => {
      if (!stopped) handlers.connected()
    }
    current.onerror = () => {
      current.close()
      if (source === current) void failed()
    }
    for (const type of eventTypes) {
      current.addEventListener(type, (message) => {
        if (stopped) return
        let data: unknown
        try {
          data = JSON.parse(message.data)
        } catch {
          return
        }
        attempt = 0
        handlers.event({ type, data } as ServerEvent)
      })
    }
  }

  async function failed() {
    source = null
    const refusal = await deps.status()
    if (stopped) return
    if (refusal?.endsSession) {
      stopped = true
      handlers.ended(refusal)
      return
    }
    if (refusal) handlers.disconnected(refusal.message)
    const delay = refusal?.status === 429 ? tooManyStreamsDelay : reconnectDelay(attempt)
    timer = deps.setTimeout(() => {
      timer = null
      attempt += 1
      connect()
    }, delay)
  }

  connect()
  return {
    close() {
      stopped = true
      source?.close()
      source = null
      if (timer !== null) deps.clearTimeout(timer)
      timer = null
    },
  }
}
