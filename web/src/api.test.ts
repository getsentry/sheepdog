import { describe, expect, it } from 'vitest'
import { ApiError, createApiClient } from './api'
import { failure, fakeFetch, reply } from './test/fakes'

const settings = { repo: 'getsentry/sentry-react-native', model: 'google/gemini-4-hydrogen', hasOpenRouterKey: true, starred: [], autoRefresh: true }
const task = {
  id: 'S6L32X34DAKG7WI2CRMWGRYNTT',
  kind: 'review',
  key: 'getsentry/sentry-react-native#6773',
  title: 'Review of #6773',
  subtitle: 'getsentry/sentry-react-native',
  startedAt: '2026-11-16T11:24:55.569814+01:00',
}
const review = {
  repo: 'getsentry/sentry-react-native',
  number: 6773,
  headSha: '03da17fea404491d637aeeacdfad4539ec229c7f',
  model: 'google/gemini-4-hydrogen',
  risk: 'yellow',
  summary: 'Updates the JavaScript SDK.',
  findings: [],
  diffTruncated: false,
  prUpdatedAt: '2026-11-16T08:20:44Z',
  createdAt: '2026-11-16T11:30:12.345+01:00',
}

describe('API client', () => {
  it('sends every request with the cookie and without a token', async () => {
    const server = fakeFetch(({ method, path }) => {
      switch (`${method} ${path}`) {
        case 'GET /api/me':
          return reply(200, { login: 'alwx' })
        case 'GET /api/settings':
        case 'PUT /api/settings':
          return reply(200, settings)
        case 'GET /api/repos':
          return reply(200, ['getsentry/sentry-dart', 'getsentry/junior-prod'])
        case 'GET /api/models':
          return reply(200, ['google/gemini-4-hydrogen'])
        case 'PUT /api/repos/getsentry/sentry-react-native/star':
          return reply(200, { ...settings, starred: ['getsentry/sentry-react-native'] })
        case 'DELETE /api/repos/getsentry/sentry-react-native/star':
          return reply(200, settings)
        case 'GET /api/review-requests':
          return reply(200, { repo: settings.repo, fetchedAt: null, rows: [], lanes: [] })
        case 'POST /api/review-requests/refresh':
          return reply(202)
        case 'GET /api/repos/getsentry/sentry-react-native/pulls/6773':
          return reply(200, { changedFiles: 7 })
        case 'GET /api/repos/getsentry/sentry-react-native/pulls/6773/review':
          return reply(200, review)
        case 'POST /api/repos/getsentry/sentry-react-native/pulls/6773/review':
        case 'POST /api/repos/getsentry/sentry-react-native/pulls/6773/classification':
          return reply(202, task)
        case 'GET /api/repos/getsentry/sentry-react-native/classifications':
          return reply(200, [])
        case 'GET /api/tasks':
          return reply(200, [task])
        case 'DELETE /api/tasks/S6L32X34DAKG7WI2CRMWGRYNTT':
        case 'DELETE /api/session':
        case 'DELETE /api/sessions':
          return reply(204)
        default:
          return failure(418, 'unexpected')
      }
    })
    const client = createApiClient(server.fetch)
    const repo = 'getsentry/sentry-react-native'
    expect(await client.me()).toBe('alwx')
    expect((await client.settings()).hasOpenRouterKey).toBe(true)
    await client.updateSettings({ model: 'google/gemini-4-hydrogen', openRouterKey: '' })
    expect(await client.repos()).toHaveLength(2)
    expect((await client.star(repo)).starred).toEqual([repo])
    expect((await client.unstar(repo)).starred).toEqual([])
    expect(await client.models()).toEqual(['google/gemini-4-hydrogen'])
    expect((await client.reviewRequests()).rows).toEqual([])
    await client.refresh()
    expect((await client.pullRequest(repo, 6773)).changedFiles).toBe(7)
    expect((await client.review(repo, 6773))?.risk).toBe('yellow')
    expect((await client.startReview(repo, 6773)).id).toBe(task.id)
    expect((await client.startClassification(repo, 6773)).id).toBe(task.id)
    expect(await client.classifications(repo)).toEqual([])
    expect(await client.tasks()).toHaveLength(1)
    await client.cancelTask(task.id)
    await client.signOut()
    await client.signOutEverywhere()

    expect(server.seen).toHaveLength(18)
    expect(server.seen.every((s) => s.init.credentials === 'same-origin')).toBe(true)
    expect(server.seen.some((s) => JSON.stringify(s.init.headers ?? {}).includes('Authorization'))).toBe(false)
    expect(server.seen.find((s) => s.method === 'PUT')?.body).toEqual({
      model: 'google/gemini-4-hydrogen',
      openRouterKey: '',
    })
  })

  it('signs in with the GitHub token only', async () => {
    const server = fakeFetch(() => reply(200, { login: 'alwx', sessionToken: 'session-token' }))
    expect((await createApiClient(server.fetch).signIn('ghp_test')).login).toBe('alwx')
    expect(server.seen.map((s) => `${s.method} ${s.path}`)).toEqual(['POST /api/session'])
    expect(server.seen[0].body).toEqual({ githubToken: 'ghp_test' })
    expect(server.seen[0].init.headers).toEqual({ 'Content-Type': 'application/json' })
  })

  it('sends the head commit to approve and merge', async () => {
    const server = fakeFetch(({ path }) =>
      path.endsWith('/12/approve-and-merge')
        ? reply(200, { result: 'merged' })
        : failure(409, 'The pull request has new commits. Check them before you approve'),
    )
    const client = createApiClient(server.fetch)
    expect(await client.approveAndMerge('acme/app', 12, 'aaa111')).toEqual({ result: 'merged' })
    await expect(client.approveAndMerge('acme/app', 13, 'bbb222')).rejects.toThrow(
      'The pull request has new commits. Check them before you approve',
    )
    expect(server.seen.map((s) => `${s.method} ${s.path}`)).toEqual([
      'POST /api/repos/acme/app/pulls/12/approve-and-merge',
      'POST /api/repos/acme/app/pulls/13/approve-and-merge',
    ])
    expect(server.seen[0].body).toEqual({ headSha: 'aaa111' })
  })

  it('loads the setup template and opens the setup pull request', async () => {
    const server = fakeFetch(({ method, path }) => {
      if (method === 'GET' && path === '/api/repos/acme/app/setup/template') return reply(200, { content: 'version: 1\n' })
      if (method === 'POST' && path === '/api/repos/acme/app/setup') return reply(201, { number: 12, url: 'https://github.com/acme/app/pull/12' })
      return failure(400, '.github/sheepdog.yml is not valid: version must be 1')
    })
    const client = createApiClient(server.fetch)
    expect(await client.setupTemplate('acme/app')).toBe('version: 1\n')
    expect(await client.setup('acme/app', 'version: 1\n')).toEqual({ number: 12, url: 'https://github.com/acme/app/pull/12' })
    await expect(client.setup('acme/other', 'version: 2\n')).rejects.toThrow('.github/sheepdog.yml is not valid: version must be 1')
    expect(server.seen.map((s) => `${s.method} ${s.path}`)).toEqual([
      'GET /api/repos/acme/app/setup/template',
      'POST /api/repos/acme/app/setup',
      'POST /api/repos/acme/other/setup',
    ])
    expect(server.seen[1].body).toEqual({ content: 'version: 1\n' })
  })

  it('keeps the server text in errors', async () => {
    const server = fakeFetch(({ method, path }) => {
      if (path === '/api/me') return failure(401, 'The session is not valid. Sign in again')
      if (path === '/api/settings') return failure(403, 'This GitHub account is not allowed on this server')
      if (method === 'POST') return failure(409, 'A review of this pull request is already running')
      if (path === '/api/models') return reply(502, '<html>Bad Gateway</html>')
      return failure(404, 'This pull request has no review')
    })
    const client = createApiClient(server.fetch)
    const error = (promise: Promise<unknown>) => promise.then(() => null, (e: ApiError) => e)

    const unauthorized = (await error(client.me()))!
    expect([unauthorized.kind, unauthorized.status, unauthorized.message]).toEqual([
      'unauthorized',
      401,
      'The session is not valid. Sign in again',
    ])
    expect(unauthorized.endsSession).toBe(true)
    const forbidden = (await error(client.settings()))!
    expect([forbidden.kind, forbidden.message]).toEqual(['forbidden', 'This GitHub account is not allowed on this server'])
    expect(forbidden.endsSession).toBe(true)
    const conflict = (await error(client.startReview('acme/app', 1)))!
    expect([conflict.kind, conflict.status, conflict.message]).toEqual([
      'server',
      409,
      'A review of this pull request is already running',
    ])
    expect(conflict.endsSession).toBe(false)
    expect((await error(client.models()))?.message).toBe('The server returned HTTP 502')
    expect(new ApiError(429, 'x').endsSession).toBe(false)
    expect(await client.review('acme/app', 1)).toBeNull()
    expect(await client.classification('acme/app', 1)).toBeNull()
  })

  it('reports a server that is not reachable', async () => {
    const client = createApiClient(async () => {
      throw new TypeError('Failed to fetch')
    })
    const error = await client.me().catch((e: ApiError) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect([(error as ApiError).kind, (error as ApiError).message]).toEqual(['network', 'The server is not reachable'])
  })

  it('reads the status of a new event stream and closes it', async () => {
    const replies = [reply(200, ''), failure(429, 'Too many open event streams. Close another client and try again')]
    const server = fakeFetch(() => replies.shift()!)
    const client = createApiClient(server.fetch)
    expect(await client.eventsStatus()).toBeNull()
    expect(server.seen[0].init.signal?.aborted).toBe(true)
    const refused = await client.eventsStatus()
    expect([refused?.status, refused?.message]).toEqual([
      429,
      'Too many open event streams. Close another client and try again',
    ])
    expect(server.seen.map((s) => `${s.method} ${s.path}`)).toEqual(['GET /api/events', 'GET /api/events'])
  })
})
