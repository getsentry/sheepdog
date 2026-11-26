import { describe, expect, it, vi } from 'vitest'
import { key } from './logic'
import { createStore, currentRepo, isClassifying, isMerging, isReviewing, isSettingUp } from './store'
import { failure, fakeEventSources, fakeFetch, fakeTimers, Gate, reply, settle, type Reply, type Seen } from './test/fakes'
import { laneRow, requests } from './test/rows'
import { approveAndMergeAction, type Classification, type Review, type RunningTask } from './types'

const notAllowed = 'This GitHub account is not allowed on this server'
const badSession = 'The session is not valid. Sign in again'
const settings = { repo: 'acme/app', model: 'a/model', hasOpenRouterKey: true }

const task: RunningTask = {
  id: 'T1',
  kind: 'review',
  key: 'acme/app#1',
  title: 'Review of #1',
  subtitle: 'acme/app',
  startedAt: '2026-11-16T10:00:00Z',
}

function review(summary: string, createdAt: string, repo = 'acme/app', number = 1): Review {
  return {
    repo,
    number,
    headSha: 'abc',
    model: 'a/model',
    risk: 'green',
    summary,
    findings: [],
    diffTruncated: false,
    prUpdatedAt: '2026-11-16T08:00:00Z',
    createdAt,
  }
}

function classification(number: number, score: number, createdAt: string, repo = 'acme/app'): Classification {
  return {
    repo,
    number,
    headSha: 'abc',
    model: 'm',
    score,
    reason: 'r',
    diffTruncated: false,
    prUpdatedAt: '2026-11-16T10:00:00Z',
    createdAt,
  }
}

// A store with a session cookie. A handler that returns undefined gives the usual reply of the server.
async function signedIn(handler: (seen: Seen) => Reply | undefined | Promise<Reply | undefined> = () => undefined) {
  const server = fakeFetch(async (seen) => {
    const custom = await handler(seen)
    if (custom) return custom
    switch (`${seen.method} ${seen.path}`) {
      case 'GET /api/me':
        return reply(200, { login: 'alwx' })
      case 'GET /api/events':
        return reply(200, '')
      case 'GET /api/settings':
      case 'PUT /api/settings':
        return reply(200, settings)
      case 'GET /api/repos/acme/app/classifications':
        return reply(200, [])
    }
    return seen.method === 'DELETE' ? reply(204) : failure(404, 'Not found')
  })
  const sources = fakeEventSources()
  const timers = fakeTimers()
  const store = createStore({ fetch: server.fetch, EventSource: sources.EventSource, ...timers })
  await store.actions.start()
  // Opens the current stream with the first events of the server.
  function connect() {
    const source = sources.last()
    source.open()
    source.emit('tasks', [])
    source.emit('requests', requests([]))
  }
  // Fails the stream, and connects again after the delay.
  async function reconnect() {
    sources.last().fail()
    await settle()
    timers.fire()
    connect()
  }
  const paths = (suffix: string) => server.seen.filter((s) => s.path.endsWith(suffix))
  return { store, state: store.getState, actions: store.actions, server, sources, timers, connect, reconnect, paths }
}

describe('store', () => {
  it('applies the events of the server', () => {
    const store = createStore({ EventSource: fakeEventSources().EventSource })
    const repo = 'getsentry/sentry-react-native'
    const state = store.getState
    store.dispatch({ type: 'event', event: { type: 'requests', data: requests([], [], repo) } })
    expect(currentRepo(state())).toBe(repo)

    store.dispatch({ type: 'event', event: { type: 'tasks', data: [{ ...task, key: `${repo}#6773` }] } })
    expect(isReviewing(state(), repo, 6773)).toBe(true)
    expect(isReviewing(state(), 'GetSentry/Sentry-React-Native', 6773)).toBe(true)
    expect(isReviewing(state(), repo, 1)).toBe(false)
    expect(isClassifying(state(), repo, 6773)).toBe(false)

    store.dispatch({ type: 'event', event: { type: 'review', data: { repo, number: 6773, error: 'Invalid API key' } } })
    expect(state().reviewErrors).toEqual({ [`${repo}#6773`]: 'Invalid API key' })
    expect(state().reviews).toEqual({})

    const done = review('Updates the JavaScript SDK.', '2026-11-16T10:00:00Z', repo, 6773)
    store.dispatch({ type: 'event', event: { type: 'review', data: { repo, number: 6773, review: done } } })
    store.dispatch({ type: 'event', event: { type: 'tasks', data: [] } })
    expect(state().reviews[key('getsentry/Sentry-React-Native', 6773)]).toEqual(done)
    expect(state().reviewErrors).toEqual({})
    expect(isReviewing(state(), repo, 6773)).toBe(false)
  })

  it('keeps classification events apart from reviews', () => {
    const store = createStore({ EventSource: fakeEventSources().EventSource })
    const state = store.getState
    const repo = 'getsentry/sentry-react-native'
    const classifying: RunningTask = { ...task, id: 'T2', kind: 'classify', key: `${repo}#6773` }
    store.dispatch({ type: 'event', event: { type: 'tasks', data: [classifying] } })
    expect(isClassifying(state(), repo, 6773)).toBe(true)
    expect(isReviewing(state(), repo, 6773)).toBe(false)

    const outcome = { repo, number: 6773 }
    store.dispatch({ type: 'event', event: { type: 'classification', data: { ...outcome, error: 'Invalid API key' } } })
    expect(state().classificationErrors[`${repo}#6773`]).toBe('Invalid API key')
    expect(state().reviewErrors).toEqual({})

    const done = classification(6773, 3, '2026-11-16T10:00:00Z', repo)
    store.dispatch({ type: 'event', event: { type: 'classification', data: { ...outcome, classification: done } } })
    store.dispatch({ type: 'event', event: { type: 'tasks', data: [] } })
    expect(state().classifications[`${repo}#6773`]).toEqual(done)
    expect(state().classificationErrors).toEqual({})
    expect(state().reviews).toEqual({})
    expect(isClassifying(state(), repo, 6773)).toBe(false)
  })

  it('starts signed out without a session cookie, and with the text of a 403', async () => {
    for (const [status, text, shown] of [
      [401, 'Sign in first', null],
      [403, notAllowed, notAllowed],
    ] as const) {
      const server = fakeFetch(() => failure(status, text))
      const sources = fakeEventSources()
      const store = createStore({ fetch: server.fetch, EventSource: sources.EventSource })
      await store.actions.start()
      expect(store.getState().auth).toBe('signedOut')
      expect(store.getState().signInError).toBe(shown)
      expect(sources.sources).toHaveLength(0)
    }
  })

  it('signs in with the GitHub token and opens the event stream', async () => {
    let signedIn = false
    const server = fakeFetch(({ method, path }) => {
      if (method === 'POST') {
        signedIn = true
        return reply(200, { login: 'alwx', sessionToken: 'ignored' })
      }
      if (!signedIn) return failure(401, 'Sign in first')
      return path === '/api/settings' ? reply(200, settings) : failure(404, 'Not found')
    })
    const sources = fakeEventSources()
    const store = createStore({ fetch: server.fetch, EventSource: sources.EventSource })
    await store.actions.start()
    expect(await store.actions.signIn('  ghp_test\n')).toBe(true)
    expect(store.getState().auth).toBe('signedIn')
    expect(store.getState().login).toBe('alwx')
    expect(server.seen.find((s) => s.method === 'POST')?.body).toEqual({ githubToken: 'ghp_test' })
    expect(sources.sources).toHaveLength(1)
    await vi.waitFor(() => expect(store.getState().settings).toEqual(settings))
    expect(JSON.stringify(store.getState())).not.toContain('ignored')
  })

  it('shows a failed sign-in with the text of the server', async () => {
    const server = fakeFetch(({ method }) =>
      method === 'POST' ? failure(400, 'The token needs the scopes repo and read:org') : failure(401, 'Sign in first'),
    )
    const store = createStore({ fetch: server.fetch, EventSource: fakeEventSources().EventSource })
    await store.actions.start()
    expect(await store.actions.signIn('ghp_test')).toBe(false)
    expect(store.getState().auth).toBe('signedOut')
    expect(store.getState().signInError).toBe('The token needs the scopes repo and read:org')
  })

  it('reloads the review of the open PR after a reconnect', async () => {
    let stored = review('Old', '2026-11-16T09:00:00Z')
    const s = await signedIn(({ path }) => (path === '/api/repos/acme/app/pulls/1/review' ? reply(200, stored) : undefined))
    s.connect()
    s.actions.select(1)
    await s.actions.loadReview('acme/app', 1)
    expect(s.state().reviews['acme/app#1'].summary).toBe('Old')

    // The review finishes on the server while the stream is down.
    stored = review('Newer', '2026-11-16T11:00:00Z')
    await s.reconnect()
    await vi.waitFor(() => expect(s.state().reviews['acme/app#1'].summary).toBe('Newer'))
    expect(s.sources.sources).toHaveLength(2)
  })

  it('shows a lost connection until the stream is back', async () => {
    let refuse = true
    const s = await signedIn(({ path }) => (path === '/api/events' && refuse ? reply(502, '') : undefined))
    s.connect()
    s.sources.last().fail()
    await vi.waitFor(() => expect(s.state().connectionError).toBe('No connection to the server. The server returned HTTP 502'))
    expect(s.state().error).toBeNull()
    refuse = false
    s.timers.fire()
    s.connect()
    expect(s.state().connectionError).toBeNull()
  })

  it('forgets the data of the user at sign-out', async () => {
    const s = await signedIn(({ method, path }) => {
      if (path === '/api/models') return reply(200, ['a/model'])
      if (method === 'PUT') return failure(500, 'Disk full')
    })
    s.connect()
    await s.actions.loadModels()
    expect(s.state().models).toEqual(['a/model'])
    await s.actions.selectModel('a/model')
    expect(s.state().settingsError).toBe('Disk full')
    expect(s.state().error).toBeNull()

    await s.actions.signOut()
    expect(s.state().auth).toBe('signedOut')
    expect([s.state().models, s.state().modelsError, s.state().settingsError, s.state().requests]).toEqual([
      null,
      null,
      null,
      null,
    ])
    expect(s.sources.last().closed).toBe(true)
    expect(s.paths('/api/session').map((p) => p.method)).toEqual(['DELETE'])
  })

  it('ends the local session before the server answers the sign-out, and signs in after the answer', async () => {
    const gate = new Gate()
    const s = await signedIn(async ({ method }) => {
      if (method !== 'DELETE') return undefined
      await gate.wait()
      return reply(204)
    })
    const signOut = s.actions.signOut()
    await settle()
    expect(s.paths('/api/session')).toHaveLength(1)
    expect(s.state().auth).toBe('signedOut')

    const signIn = s.actions.signIn('ghp_test')
    await settle()
    expect(s.server.seen.some((seen) => seen.method === 'POST')).toBe(false)
    gate.open()
    await signOut
    await signIn
    expect(s.server.seen.map((seen) => seen.method).slice(-2)).toEqual(['DELETE', 'POST'])
    expect(s.state().signInError).toBe('Not found')
  })

  it('signs out on all devices, and keeps the session when that fails', async () => {
    let fails = true
    const s = await signedIn(({ path }) => {
      if (path === '/api/sessions') return fails ? failure(500, 'Internal server error') : reply(204)
    })
    s.connect()
    await s.actions.signOutEverywhere()
    expect(s.state().auth).toBe('signedIn')
    expect(s.state().settingsError).toBe('Internal server error')
    expect(s.sources.sources).toHaveLength(2)
    expect(s.sources.sources[0].closed).toBe(true)

    fails = false
    await s.actions.signOutEverywhere()
    expect(s.state().auth).toBe('signedOut')
    expect(s.state().signInError).toBeNull()
    expect(s.sources.sources.every((source) => source.closed)).toBe(true)
    expect(s.paths('/api/sessions').map((p) => p.method)).toEqual(['DELETE', 'DELETE'])
  })

  it('loads the repositories again after a failure', async () => {
    let loads = 0
    const s = await signedIn(({ path }) => {
      if (path !== '/api/repos') return undefined
      loads += 1
      return loads <= 2 ? failure(502, 'GitHub is not available') : reply(200, ['acme/app'])
    })
    s.connect()
    await s.actions.loadRepositories()
    expect(s.state().repositoriesError).toBe('GitHub is not available')
    // The picker opens again.
    await s.actions.loadRepositories()
    expect(loads).toBe(2)
    expect(s.state().repositories).toBeNull()

    await s.reconnect()
    await vi.waitFor(() => expect(s.state().repositories).toEqual(['acme/app']))
    expect(s.state().repositoriesError).toBeNull()
  })

  it('sends one request for repository loads at the same time', async () => {
    const gate = new Gate()
    let loads = 0
    const s = await signedIn(async ({ path }) => {
      if (path !== '/api/repos') return undefined
      loads += 1
      await gate.wait()
      return reply(200, ['acme/app'])
    })
    const first = s.actions.loadRepositories()
    await s.actions.loadRepositories()
    gate.open()
    await first
    expect(loads).toBe(1)
    expect(s.state().repositories).toEqual(['acme/app'])
  })

  it('keeps review errors that a reload cannot replace', async () => {
    const gate = new Gate()
    let calls = 0
    const s = await signedIn(async ({ path }) => {
      if (path !== '/api/repos/acme/app/pulls/1/review') return undefined
      calls += 1
      if (calls === 1) return failure(404, 'No review')
      if (calls === 2) await gate.wait()
      return reply(200, review('Old', '2026-11-16T09:00:00Z'))
    })
    const outcome = { repo: 'acme/app', number: 1 }
    s.store.dispatch({ type: 'event', event: { type: 'review', data: { ...outcome, error: 'Invalid API key' } } })
    await s.actions.loadReview('acme/app', 1)
    expect(s.state().reviewErrors['acme/app#1']).toBe('Invalid API key')

    const load = s.actions.loadReview('acme/app', 1)
    await settle()
    s.store.dispatch({ type: 'event', event: { type: 'review', data: { ...outcome, error: 'GitHub: Not Found' } } })
    gate.open()
    await load
    expect(s.state().reviews['acme/app#1'].summary).toBe('Old')
    expect(s.state().reviewErrors['acme/app#1']).toBe('GitHub: Not Found')

    await s.actions.loadReview('acme/app', 1)
    expect(s.state().reviewErrors).toEqual({})
  })

  it('keeps a newer review when an older one loads late', async () => {
    const s = await signedIn(({ path }) =>
      path.endsWith('/review') ? reply(200, review('Old', '2026-11-16T09:00:00Z')) : undefined,
    )
    const newer = review('New', '2026-11-16T10:00:00Z')
    s.store.dispatch({ type: 'event', event: { type: 'review', data: { repo: 'acme/app', number: 1, review: newer } } })
    await s.actions.loadReview('acme/app', 1)
    expect(s.state().reviews['acme/app#1']).toEqual(newer)
  })

  it('drops late results of an old session', async () => {
    const gate = new Gate()
    let session = 'A'
    let held = 0
    const s = await signedIn(async ({ method, path }) => {
      const owner = session
      if (owner === 'A' && method === 'PUT' && path === '/api/settings') {
        held += 1
        await gate.wait()
        return reply(200, { repo: 'acme/old', model: '', hasOpenRouterKey: false })
      }
      if (owner === 'A' && path === '/api/repos') {
        held += 1
        await gate.wait()
        return failure(401, badSession)
      }
      if (owner === 'B' && path === '/api/settings') return reply(200, { repo: 'bob/new', model: '', hasOpenRouterKey: false })
    })
    const select = s.actions.selectRepo('acme/old')
    const repos = s.actions.loadRepositories()
    await settle()
    expect(held).toBe(2)

    await s.actions.signOut()
    session = 'B'
    await s.actions.start()
    await vi.waitFor(() => expect(s.state().settings?.repo).toBe('bob/new'))

    gate.open()
    await select
    await repos
    expect(s.state().settings?.repo).toBe('bob/new')
    expect(s.state().auth).toBe('signedIn')
    expect([s.state().repositories, s.state().repositoriesError, s.state().error, s.state().signInError]).toEqual([
      null,
      null,
      null,
      null,
    ])
    expect(s.sources.last().closed).toBe(false)
  })

  it('does not keep a review running when it fails before the start returns', async () => {
    const gate = new Gate()
    const s = await signedIn(async ({ method }) => {
      if (method !== 'POST') return undefined
      await gate.wait()
      return reply(202, task)
    })
    s.connect()
    const start = s.actions.startReview('acme/app', 1)
    expect(isReviewing(s.state(), 'acme/app', 1)).toBe(true)

    // The events of the start and of the failure arrive before the POST response.
    const source = s.sources.last()
    source.emit('tasks', [task])
    source.emit('review', { repo: 'acme/app', number: 1, error: 'GitHub: Not Found' })
    source.emit('tasks', [])
    gate.open()
    await start
    expect(isReviewing(s.state(), 'acme/app', 1)).toBe(false)
    expect(s.state().tasks).toEqual([])
    expect(s.state().reviewErrors['acme/app#1']).toBe('GitHub: Not Found')
  })

  it('sends one request for a double click on Review and on Classify', async () => {
    const gate = new Gate()
    const posts: string[] = []
    const s = await signedIn(async ({ method, path }) => {
      if (method !== 'POST') return undefined
      posts.push(path)
      await gate.wait()
      return reply(202, path.endsWith('/review') ? task : { ...task, id: 'T2', kind: 'classify' })
    })
    s.connect()
    const review = s.actions.startReview('acme/app', 1)
    await s.actions.startReview('acme/app', 1)
    const classify = s.actions.startClassification('acme/app', 1)
    await s.actions.startClassification('acme/app', 1)
    gate.open()
    await review
    await classify
    expect(posts).toEqual(['/api/repos/acme/app/pulls/1/review', '/api/repos/acme/app/pulls/1/classification'])
    expect(s.state().reviewErrors).toEqual({})
    // No tasks event came after the start, so the tasks from the responses show.
    expect(s.state().tasks.map((t) => t.id)).toEqual(['T1', 'T2'])
    expect(isReviewing(s.state(), 'acme/app', 1)).toBe(true)
    expect(isClassifying(s.state(), 'acme/app', 1)).toBe(true)
    expect(s.state().startingReviews.size + s.state().startingClassifications.size).toBe(0)
  })

  it('loads the classifications of the repository, and again after a reconnect', async () => {
    let loads = 0
    const s = await signedIn(({ path }) => {
      if (path !== '/api/repos/acme/app/classifications') return undefined
      loads += 1
      const first = loads === 1
      return reply(200, [
        classification(1, first ? 2 : 9, first ? '2026-11-16T10:00:00Z' : '2026-11-16T11:00:00Z'),
        classification(4, 6, '2026-11-16T10:00:00Z'),
      ])
    })
    s.connect()
    await vi.waitFor(() => expect(s.state().classifications['acme/app#4']?.score).toBe(6))
    expect(s.state().classifications['acme/app#1'].score).toBe(2)
    s.sources.last().emit('requests', requests([]))
    await settle()
    expect(loads).toBe(1)

    await s.reconnect()
    await vi.waitFor(() => expect(s.state().classifications['acme/app#1'].score).toBe(9))
    expect(loads).toBe(2)
    expect(s.state().error).toBeNull()
    await s.actions.signOut()
    expect(s.state().classifications).toEqual({})
  })

  it('loads and starts a classification', async () => {
    const s = await signedIn(({ method, path }) => {
      if (method === 'GET' && path === '/api/repos/acme/app/pulls/1/classification') {
        return reply(200, { ...classification(1, 8, '2026-11-16T10:30:12.345Z'), diffTruncated: true })
      }
      if (method === 'POST' && path === '/api/repos/acme/app/pulls/2/classification') {
        return failure(409, 'A classification of this pull request is already running')
      }
    })
    s.connect()
    await s.actions.loadClassification('acme/app', 1)
    expect(s.state().classifications['acme/app#1'].score).toBe(8)
    expect(s.state().classifications['acme/app#1'].diffTruncated).toBe(true)
    await s.actions.startClassification('acme/app', 2)
    expect(s.state().classificationErrors['acme/app#2']).toBe('A classification of this pull request is already running')
    expect(s.state().reviewErrors).toEqual({})
  })

  it('ends the session when the account is no longer allowed', async () => {
    const stream = await signedIn(({ path }) => (path === '/api/events' ? failure(403, notAllowed) : undefined))
    stream.connect()
    stream.sources.last().fail()
    await vi.waitFor(() => expect(stream.state().auth).toBe('signedOut'))
    expect(stream.state().signInError).toBe(notAllowed)
    expect(stream.timers.pending.size).toBe(0)

    const route = await signedIn(({ path }) => (path === '/api/repos' ? failure(403, notAllowed) : undefined))
    await route.actions.loadRepositories()
    expect(route.state().auth).toBe('signedOut')
    expect(route.state().signInError).toBe(notAllowed)
    expect(route.state().repositoriesError).toBeNull()
    expect(route.sources.last().closed).toBe(true)
  })

  it('runs approve and merge one after another, and goes on after a failure', async () => {
    const gate = new Gate()
    const seen: string[] = []
    let running = 0
    let most = 0
    const s = await signedIn(async ({ path, body }) => {
      if (!path.endsWith('/approve-and-merge')) return undefined
      seen.push(`${path} ${(body as { headSha: string }).headSha}`)
      running += 1
      most = Math.max(most, running)
      if (path.includes('/pulls/1/')) await gate.wait()
      running -= 1
      return path.includes('/pulls/2/')
        ? failure(502, 'GitHub: Can not approve your own pull request')
        : reply(200, { result: 'merged' })
    })
    s.connect()
    const rows = [1, 2, 3].map((n) => laneRow(n, 'safe', approveAndMergeAction))
    const batch = s.actions.approveAndMerge(rows)
    await settle()
    expect(seen).toHaveLength(1)
    expect(isMerging(s.state(), 'acme/app', 1)).toBe(true)
    expect(isMerging(s.state(), 'acme/app', 2)).toBe(false)
    // A second click on the running PR sends nothing.
    await s.actions.approveAndMerge([rows[0]])
    gate.open()
    await batch

    expect(seen).toEqual([
      '/api/repos/acme/app/pulls/1/approve-and-merge sha1',
      '/api/repos/acme/app/pulls/2/approve-and-merge sha2',
      '/api/repos/acme/app/pulls/3/approve-and-merge sha3',
    ])
    expect(most).toBe(1)
    expect(s.state().error).toBe('Approve and merge failed.\n#2: GitHub: Can not approve your own pull request')
    expect(s.state().merging.size).toBe(0)
  })

  it('removes a stopped task, also when the server no longer knows it', async () => {
    const s = await signedIn(({ path }) => (path === '/api/tasks/T2' ? failure(404, 'No running task has this ID') : undefined))
    s.connect()
    s.sources.last().emit('tasks', [task, { ...task, id: 'T2' }, { ...task, id: 'T3' }])
    await s.actions.cancelTask('T1')
    await s.actions.cancelTask('T2')
    expect(s.state().tasks.map((t) => t.id)).toEqual(['T3'])
    expect(s.state().error).toBeNull()
  })

  it('loads no classifications for a repository without rules, and hides the 409 that comes before the setup', async () => {
    const gated = 'acme/new has no .github/sheepdog.yml on main. Set it up first'
    const s = await signedIn(({ path }) => (path === '/api/repos/acme/new/classifications' ? failure(409, gated) : undefined))
    s.connect()
    s.actions.select(1)
    const setup = { defaultBranch: 'main', pullRequest: null }
    s.sources.last().emit('requests', { ...requests([]), setup })
    expect(s.state().selection).toBeNull()

    // A change of the repository first sends an empty result without setup.
    s.sources.last().emit('requests', requests([], [], 'acme/new'))
    await settle()
    s.sources.last().emit('requests', { ...requests([], [], 'acme/new'), setup })
    await settle()
    expect(s.paths('/api/repos/acme/new/classifications')).toHaveLength(1)
    expect(s.state().error).toBeNull()

    // The merged setup PR gives a result without setup, so the classifications load again.
    s.sources.last().emit('requests', requests([], [], 'acme/new'))
    await settle()
    expect(s.paths('/api/repos/acme/new/classifications')).toHaveLength(2)
  })

  it('sends one setup at a time, and drops the setup result of an old session', async () => {
    const gate = new Gate()
    const pr = { number: 12, url: 'https://github.com/acme/new/pull/12' }
    const s = await signedIn(async ({ method, path }) => {
      if (method !== 'POST' || path !== '/api/repos/acme/new/setup') return undefined
      await gate.wait()
      return reply(201, pr)
    })
    const first = s.actions.setUp('acme/new', 'version: 1\n')
    expect(isSettingUp(s.state(), 'ACME/new')).toBe(true)
    expect(await s.actions.setUp('acme/new', 'version: 1\n')).toBeNull()
    gate.open()
    expect(await first).toEqual(pr)
    expect(isSettingUp(s.state(), 'acme/new')).toBe(false)
    expect(s.paths('/api/repos/acme/new/setup').map((p) => p.body)).toEqual([{ content: 'version: 1\n' }])

    const held = new Gate()
    const late = await signedIn(async ({ path }) => {
      if (path !== '/api/repos/acme/new/setup/template' && path !== '/api/repos/acme/new/setup') return undefined
      await held.wait()
      return path.endsWith('/template') ? reply(200, { content: 'version: 1\n' }) : reply(201, pr)
    })
    const template = late.actions.setupTemplate('acme/new')
    const setUp = late.actions.setUp('acme/new', 'version: 1\n')
    await late.actions.signOut()
    await late.actions.start()
    held.open()
    expect(await template).toBeNull()
    expect(await setUp).toBeNull()
    expect(late.state().settingUp.size).toBe(0)
  })

  it('closes the open PR when the repository changes', async () => {
    const s = await signedIn()
    s.connect()
    s.actions.select(1)
    s.sources.last().emit('requests', requests([], [], 'ACME/app'))
    expect(s.state().selection).toBe(1)
    s.sources.last().emit('requests', requests([], [], 'acme/other'))
    expect(s.state().selection).toBeNull()
  })
})
