import type {
  Classification,
  MergeResult,
  PullLink,
  Preference,
  PullRequest,
  RepoSettings,
  Requests,
  Review,
  RunningTask,
  Session,
  Settings,
  SettingsUpdate,
} from './types'

export type ApiErrorKind = 'unauthorized' | 'forbidden' | 'server' | 'network'

export class ApiError extends Error {
  readonly status: number
  readonly kind: ApiErrorKind

  // status is 0 when the request reached no server.
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.kind = status === 0 ? 'network' : status === 401 ? 'unauthorized' : status === 403 ? 'forbidden' : 'server'
  }

  // The session ended or the GitHub account is no longer allowed, so the user must sign in again.
  get endsSession(): boolean {
    return this.kind === 'unauthorized' || this.kind === 'forbidden'
  }
}

const unreachable = 'The server is not reachable'

async function errorFrom(response: Response): Promise<ApiError> {
  let message = `The server returned HTTP ${response.status}`
  try {
    const body: unknown = await response.json()
    if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') message = body.error
  } catch {
    // The body is not the JSON error of the server, for example a page of a proxy.
  }
  return new ApiError(response.status, message)
}

export type ApiClient = ReturnType<typeof createApiClient>

// The browser sends the HttpOnly session cookie, so the client holds no token.
export function createApiClient(fetcher: typeof fetch = (input, init) => fetch(input, init)) {
  async function send(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<Response> {
    let response: Response
    try {
      response = await fetcher(path, {
        method,
        credentials: 'same-origin',
        headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal,
      })
    } catch {
      throw new ApiError(0, unreachable)
    }
    if (!response.ok) throw await errorFrom(response)
    return response
  }

  async function json<T>(method: string, path: string, body?: unknown): Promise<T> {
    return (await send(method, path, body)).json() as Promise<T>
  }

  // null when the server has no stored result.
  async function orNull<T>(path: string): Promise<T | null> {
    try {
      return await json<T>('GET', path)
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) return null
      throw error
    }
  }

  const pull = (repo: string, number: number) => `/api/repos/${repo}/pulls/${number}`

  return {
    signIn: (githubToken: string) => json<Session>('POST', '/api/session', { githubToken }),
    signOut: async () => void (await send('DELETE', '/api/session')),
    signOutEverywhere: async () => void (await send('DELETE', '/api/sessions')),
    me: async () => (await json<{ login: string }>('GET', '/api/me')).login,
    settings: () => json<Settings>('GET', '/api/settings'),
    updateSettings: (update: SettingsUpdate) => json<Settings>('PUT', '/api/settings', update),
    repos: () => json<string[]>('GET', '/api/repos'),
    repoSettings: (repo: string) => json<RepoSettings>('GET', `/api/repos/${repo}/settings`),
    updateRepoSettings: (repo: string, update: Partial<Record<Preference, boolean>>) =>
      json<RepoSettings>('PUT', `/api/repos/${repo}/settings`, update),
    star: (repo: string) => json<Settings>('PUT', `/api/repos/${repo}/star`),
    unstar: (repo: string) => json<Settings>('DELETE', `/api/repos/${repo}/star`),
    models: () => json<string[]>('GET', '/api/models'),
    reviewRequests: () => json<Requests>('GET', '/api/review-requests'),
    // The result comes as the next requests event.
    refresh: async () => void (await send('POST', '/api/review-requests/refresh')),
    // repo is "owner/name".
    pullRequest: (repo: string, number: number) => json<PullRequest>('GET', pull(repo, number)),
    review: (repo: string, number: number) => orNull<Review>(`${pull(repo, number)}/review`),
    startReview: (repo: string, number: number) => json<RunningTask>('POST', `${pull(repo, number)}/review`),
    classifications: (repo: string) => json<Classification[]>('GET', `/api/repos/${repo}/classifications`),
    classification: (repo: string, number: number) => orNull<Classification>(`${pull(repo, number)}/classification`),
    startClassification: (repo: string, number: number) =>
      json<RunningTask>('POST', `${pull(repo, number)}/classification`),
    // The server checks the lane again, and refuses when the head commit is no longer headSha.
    approveAndMerge: (repo: string, number: number, headSha: string) =>
      json<MergeResult>('POST', `${pull(repo, number)}/approve-and-merge`, { headSha }),
    setupTemplate: async (repo: string) =>
      (await json<{ content: string }>('GET', `/api/repos/${repo}/setup/template`)).content,
    // Opens a PR that adds .github/sheepdog.yml with this content.
    setup: (repo: string, content: string) => json<PullLink>('POST', `/api/repos/${repo}/setup`, { content }),
    tasks: () => json<RunningTask[]>('GET', '/api/tasks'),
    cancelTask: async (id: string) => void (await send('DELETE', `/api/tasks/${encodeURIComponent(id)}`)),
    // EventSource does not show the status of a refused stream, so this request reads it and closes the stream at once.
    // It returns null when the server accepts a stream.
    eventsStatus: async (): Promise<ApiError | null> => {
      const abort = new AbortController()
      try {
        await send('GET', '/api/events', undefined, abort.signal)
        return null
      } catch (error) {
        return error as ApiError
      } finally {
        abort.abort()
      }
    },
  }
}
