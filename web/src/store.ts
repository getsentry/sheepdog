import { ApiError, createApiClient, type ApiClient } from './api'
import { openEventStream, type EventSourceLike, type StreamDeps } from './events'
import { key, time } from './logic'
import { showNotification, type DesktopNotification } from './notifications'
import type {
  Classification,
  PullDiff,
  PullLink,
  PullRequest,
  RepoSettings,
  Review,
  Row,
  RunningTask,
  Preference,
  ServerEvent,
  Settings,
  Requests,
} from './types'

export type Auth = 'checking' | 'signedOut' | 'signedIn'

export interface State {
  // Changes with each session, so that late results of an old session are dropped.
  generation: number
  auth: Auth
  login: string | null
  signInError: string | null
  settings: Settings | null
  settingsError: string | null
  models: string[] | null
  modelsError: string | null
  repositories: string[] | null
  repositoriesError: string | null
  // The last failed star change in the repository picker.
  starError: string | null
  // The last poll of the server. null until the event stream sends it.
  requests: Requests | null
  // From a click on refresh until the next requests event.
  refreshing: boolean
  refreshError: string | null
  // The settings of the repository whose settings dialog opened last.
  repoSettings: RepoSettings | null
  repoSettingsError: string | null
  selection: number | null
  // Set while the event stream is down.
  connectionError: string | null
  // The last failed action in the main window.
  error: string | null
  tasks: RunningTask[]
  // Keyed by "owner/name#number".
  reviews: Record<string, Review>
  reviewErrors: Record<string, string>
  classifications: Record<string, Classification>
  classificationErrors: Record<string, string>
  // The starts that wait for the server, so that a second click sends nothing.
  startingReviews: ReadonlySet<string>
  startingClassifications: ReadonlySet<string>
  merging: ReadonlySet<string>
  // Keyed by the repository in lower case.
  settingUp: ReadonlySet<string>
  starring: ReadonlySet<string>
  savingPreferences: ReadonlySet<string>
  // Keyed by "owner/name#number".
  postingReviews: ReadonlySet<string>
}

type Field =
  | 'signInError'
  | 'settings'
  | 'settingsError'
  | 'models'
  | 'modelsError'
  | 'repositories'
  | 'repositoriesError'
  | 'starError'
  | 'refreshing'
  | 'refreshError'
  | 'repoSettings'
  | 'repoSettingsError'
  | 'selection'
  | 'connectionError'
  | 'error'

type Pending =
  | 'startingReviews'
  | 'startingClassifications'
  | 'merging'
  | 'settingUp'
  | 'starring'
  | 'savingPreferences'
  | 'postingReviews'

export type Action =
  | { type: 'signedIn'; login: string }
  | { type: 'signedOut'; message: string | null }
  | { type: 'set'; values: Partial<Pick<State, Field>> }
  | { type: 'event'; event: ServerEvent }
  | { type: 'taskStarted'; task: RunningTask }
  | { type: 'taskCancelled'; id: string }
  | { type: 'pending'; set: Pending; key: string; on: boolean }
  | { type: 'star'; repo: string; on: boolean }
  // A load keeps a newer result, and keeps an error that came while it ran.
  | { type: 'reviewLoaded'; key: string; review: Review | null; previousError: string | undefined }
  | { type: 'reviewError'; key: string; error: string | null }
  | {
      type: 'classificationLoaded'
      key: string
      classification: Classification | null
      previousError: string | undefined
    }
  | { type: 'classificationsLoaded'; list: Classification[] }
  | { type: 'reviewsLoaded'; list: Review[] }
  | { type: 'classificationError'; key: string; error: string | null }

export const initialState: State = {
  generation: 0,
  auth: 'checking',
  login: null,
  signInError: null,
  settings: null,
  settingsError: null,
  models: null,
  modelsError: null,
  repositories: null,
  repositoriesError: null,
  starError: null,
  requests: null,
  refreshing: false,
  refreshError: null,
  repoSettings: null,
  repoSettingsError: null,
  selection: null,
  connectionError: null,
  error: null,
  tasks: [],
  reviews: {},
  reviewErrors: {},
  classifications: {},
  classificationErrors: {},
  startingReviews: new Set(),
  startingClassifications: new Set(),
  merging: new Set(),
  settingUp: new Set(),
  starring: new Set(),
  savingPreferences: new Set(),
  postingReviews: new Set(),
}

export function currentRepo(state: State): string {
  return state.requests?.repo ?? state.settings?.repo ?? ''
}

// True when the repository has neither .github/sheepdog.yml nor global rules, so Sheepdog shows no PRs for it.
export function hasNoRules(requests: Requests | null): boolean {
  return Boolean(requests?.setup && !requests.globalRules)
}

export function isReviewing(state: State, repo: string, number: number): boolean {
  const k = key(repo, number)
  return state.startingReviews.has(k) || state.tasks.some((t) => t.kind === 'review' && t.key.toLowerCase() === k)
}

export function isClassifying(state: State, repo: string, number: number): boolean {
  const k = key(repo, number)
  return (
    state.startingClassifications.has(k) || state.tasks.some((t) => t.kind === 'classify' && t.key.toLowerCase() === k)
  )
}

export function isMerging(state: State, repo: string, number: number): boolean {
  return state.merging.has(key(repo, number))
}

export function isPostingReview(state: State, repo: string, number: number): boolean {
  return state.postingReviews.has(key(repo, number))
}

export function isSettingUp(state: State, repo: string): boolean {
  return state.settingUp.has(repo.toLowerCase())
}

export function isStarred(state: State, repo: string): boolean {
  return state.settings?.starred.includes(repo.toLowerCase()) ?? false
}

export function isAutoClassified(state: State, repo: string): boolean {
  return state.settings?.autoClassifyRepos.includes(repo.toLowerCase()) ?? false
}

export function isAutoReviewed(state: State, repo: string): boolean {
  return state.settings?.autoReviewRepos.includes(repo.toLowerCase()) ?? false
}

// The list in the settings that the repository picker uses for each switch.
const repoLists = { autoClassify: 'autoClassifyRepos', autoReview: 'autoReviewRepos' } as const

function without<T>(record: Record<string, T>, k: string): Record<string, T> {
  if (!(k in record)) return record
  const { [k]: _, ...rest } = record
  return rest
}

function withError(record: Record<string, string>, k: string, error: string | null): Record<string, string> {
  return error === null ? without(record, k) : { ...record, [k]: error }
}

function newer<T extends { createdAt: string }>(value: T, current: T | undefined): boolean {
  return !current || time(value.createdAt) >= time(current.createdAt)
}

function applyEvent(state: State, event: ServerEvent): State {
  switch (event.type) {
    case 'requests':
      return { ...state, requests: event.data, refreshing: false }
    case 'tasks':
      return { ...state, tasks: event.data }
    case 'review': {
      const k = key(event.data.repo, event.data.number)
      if (event.data.review) {
        return {
          ...state,
          reviews: { ...state.reviews, [k]: event.data.review },
          reviewErrors: without(state.reviewErrors, k),
        }
      }
      if (event.data.error) return { ...state, reviewErrors: { ...state.reviewErrors, [k]: event.data.error } }
      return state
    }
    case 'classification': {
      const k = key(event.data.repo, event.data.number)
      if (event.data.classification) {
        return {
          ...state,
          classifications: { ...state.classifications, [k]: event.data.classification },
          classificationErrors: without(state.classificationErrors, k),
        }
      }
      if (event.data.error) {
        return { ...state, classificationErrors: { ...state.classificationErrors, [k]: event.data.error } }
      }
      return state
    }
  }
}

function update(state: State, action: Action): State {
  switch (action.type) {
    case 'signedIn':
      return { ...initialState, generation: state.generation + 1, auth: 'signedIn', login: action.login }
    case 'signedOut':
      return { ...initialState, generation: state.generation + 1, auth: 'signedOut', signInError: action.message }
    case 'set':
      return { ...state, ...action.values }
    case 'event':
      return applyEvent(state, action.event)
    case 'taskStarted':
      return state.tasks.some((t) => t.id === action.task.id) ? state : { ...state, tasks: [...state.tasks, action.task] }
    case 'taskCancelled':
      return { ...state, tasks: state.tasks.filter((t) => t.id !== action.id) }
    case 'pending': {
      const set = new Set(state[action.set])
      if (action.on) set.add(action.key)
      else set.delete(action.key)
      return { ...state, [action.set]: set }
    }
    case 'star': {
      if (!state.settings) return state
      const starred = state.settings.starred.filter((repo) => repo !== action.repo)
      if (action.on) starred.push(action.repo)
      return { ...state, settings: { ...state.settings, starred: starred.sort() } }
    }
    case 'reviewLoaded': {
      let next = state
      if (action.review && newer(action.review, state.reviews[action.key])) {
        next = { ...next, reviews: { ...next.reviews, [action.key]: action.review } }
      }
      // The server does not store failed reviews, so a load that finds no review keeps the error.
      if (action.review && next.reviewErrors[action.key] === action.previousError) {
        next = { ...next, reviewErrors: without(next.reviewErrors, action.key) }
      }
      return next
    }
    case 'reviewError':
      return { ...state, reviewErrors: withError(state.reviewErrors, action.key, action.error) }
    case 'classificationLoaded': {
      let next = state
      if (action.classification && newer(action.classification, state.classifications[action.key])) {
        next = { ...next, classifications: { ...next.classifications, [action.key]: action.classification } }
      }
      if (action.classification && next.classificationErrors[action.key] === action.previousError) {
        next = { ...next, classificationErrors: without(next.classificationErrors, action.key) }
      }
      return next
    }
    case 'classificationsLoaded': {
      const classifications = { ...state.classifications }
      for (const c of action.list) {
        const k = key(c.repo, c.number)
        if (newer(c, classifications[k])) classifications[k] = c
      }
      return { ...state, classifications }
    }
    case 'classificationError':
      return { ...state, classificationErrors: withError(state.classificationErrors, action.key, action.error) }
    case 'reviewsLoaded': {
      const reviews = { ...state.reviews }
      for (const r of action.list) {
        const k = key(r.repo, r.number)
        if (newer(r, reviews[k])) reviews[k] = r
      }
      return { ...state, reviews }
    }
  }
}

// The open PR belongs to one repository, so a change of the repository closes it. A repository without rules has no PRs to open.
export function reducer(state: State, action: Action): State {
  const next = update(state, action)
  const otherRepo = currentRepo(next).toLowerCase() !== currentRepo(state).toLowerCase()
  if (next.selection !== null && (otherRepo || hasNoRules(next.requests))) {
    return { ...next, selection: null }
  }
  return next
}

export interface StoreDeps {
  fetch: typeof fetch
  EventSource: new (url: string) => EventSourceLike
  setTimeout: StreamDeps['setTimeout']
  clearTimeout: StreamDeps['clearTimeout']
  notify: (notification: DesktopNotification) => void
}

// A result that no longer matters: a result of an ended session, or a 401 or 403 that ended the session.
class Cancelled extends Error {}

export type Store = ReturnType<typeof createStore>

export function createStore(deps: Partial<StoreDeps> = {}) {
  const api: ApiClient = createApiClient(deps.fetch)
  const streamDeps: StreamDeps = {
    EventSource: deps.EventSource ?? EventSource,
    status: () => api.eventsStatus(),
    setTimeout: deps.setTimeout ?? ((callback, ms) => setTimeout(callback, ms)),
    clearTimeout: deps.clearTimeout ?? ((timer) => clearTimeout(timer as ReturnType<typeof setTimeout>)),
  }

  let state = initialState
  const listeners = new Set<() => void>()
  let stream: { close(): void } | null = null
  // The repository whose stored reviews and classifications were loaded for the list. null after a reconnect, so the next requests event loads them again.
  let listsRepo: string | null = null
  let tasksVersion = 0
  // The generation of a running load of the repositories.
  let loadingRepositories: number | null = null
  // The repository of the latest load of repository settings.
  let loadingRepoSettings = ''
  let signingOut: Promise<void> = Promise.resolve()
  const notify = deps.notify ?? showNotification
  // The PRs and the tasks that the client knew before the last event, or null when it knows none yet, so that a first list sends no notification.
  let knownRows: { repo: string; numbers: Set<number> } | null = null
  let knownTasks: Set<string> | null = null
  // "kind key" of the tasks that this client started, so that a click sends no notification.
  const ownStarts = new Set<string>()

  const getState = () => state

  function dispatch(action: Action) {
    state = reducer(state, action)
    for (const listener of listeners) listener()
  }

  const set = (values: Partial<Pick<State, Field>>) => dispatch({ type: 'set', values })

  function message(error: unknown): string {
    return error instanceof Error ? error.message : String(error)
  }

  function endSession(text: string | null) {
    stream?.close()
    stream = null
    listsRepo = null
    knownRows = null
    knownTasks = null
    dispatch({ type: 'signedOut', message: text })
  }

  async function checked<T>(body: (client: ApiClient) => Promise<T>): Promise<T> {
    if (state.auth !== 'signedIn') throw new Cancelled()
    const generation = state.generation
    try {
      const value = await body(api)
      if (generation !== state.generation) throw new Cancelled()
      return value
    } catch (error) {
      if (error instanceof Cancelled) throw error
      if (error instanceof ApiError && error.endsSession) {
        if (generation === state.generation) endSession(error.message)
        throw new Cancelled()
      }
      if (generation !== state.generation) throw new Cancelled()
      throw error
    }
  }

  // Clears the error field, and sets it on a failure.
  async function call<T>(field: 'settingsError' | 'error', body: (client: ApiClient) => Promise<T>): Promise<T | null> {
    set({ [field]: null })
    try {
      return await checked(body)
    } catch (error) {
      if (!(error instanceof Cancelled)) set({ [field]: message(error) })
      return null
    }
  }

  function startStream() {
    const generation = state.generation
    const current = () => state.generation === generation
    stream = openEventStream(
      {
        connected() {
          if (!current()) return
          set({ connectionError: null })
          listsRepo = null
          // The first tasks event of a stream lists all running tasks.
          knownTasks = null
          if (state.repositoriesError !== null) void actions.loadRepositories()
          // The server does not send again the review and classification events that the client missed while the stream was down.
          const repo = currentRepo(state)
          if (state.selection !== null && repo) {
            void actions.loadReview(repo, state.selection)
            void actions.loadClassification(repo, state.selection)
          }
        },
        event(event) {
          if (!current()) return
          if (event.type === 'tasks') tasksVersion += 1
          dispatch({ type: 'event', event })
          if (event.type === 'requests') notifyNewRows(event.data)
          if (event.type === 'tasks') notifyNewTasks(event.data)
          // The server refuses the reviews and classifications of a repository without rules.
          const repo = event.type === 'requests' && !hasNoRules(event.data) ? event.data.repo : ''
          if (repo && repo.toLowerCase() !== listsRepo) {
            listsRepo = repo.toLowerCase()
            void actions.loadClassifications(repo)
            void actions.loadReviews(repo)
          }
        },
        disconnected(text) {
          if (current()) set({ connectionError: `No connection to the server. ${text}` })
        },
        ended(error) {
          if (current()) endSession(error.message)
        },
      },
      streamDeps,
    )
  }

  function open(login: string) {
    knownRows = null
    knownTasks = null
    dispatch({ type: 'signedIn', login })
    void actions.loadSettings()
    startStream()
  }

  // A poll that restarts sends an empty list without fetchedAt, and a new repository sends all its PRs, so neither sends notifications.
  function notifyNewRows(data: Requests) {
    const known = knownRows
    const repo = data.repo.toLowerCase()
    knownRows = data.fetchedAt === null ? null : { repo, numbers: new Set(data.rows.map((row) => row.number)) }
    if (!known || data.fetchedAt === null || known.repo !== repo) return
    for (const row of data.rows) {
      if (known.numbers.has(row.number)) continue
      notify({
        title: `New review request in ${data.repo}`,
        body: `#${row.number} ${row.title} by ${row.author}`,
        tag: `sheepdog-pr-${key(data.repo, row.number)}`,
        onClick: () => actions.select(row.number),
      })
    }
  }

  function notifyNewTasks(list: RunningTask[]) {
    const known = knownTasks
    knownTasks = new Set(list.map((task) => task.id))
    if (!known) return
    for (const task of list) {
      if (known.has(task.id) || ownStarts.delete(`${task.kind} ${task.key.toLowerCase()}`)) continue
      const repo = task.subtitle
      const number = Number(task.key.slice(task.key.lastIndexOf('#') + 1))
      const shown = currentRepo(state).toLowerCase() === repo.toLowerCase()
      const row = shown ? state.requests?.rows.find((item) => item.number === number) : undefined
      notify({
        title: task.kind === 'review' ? 'Review started' : 'Classification started',
        body: `${repo} #${number}${row ? ` ${row.title}` : ''}`,
        tag: `sheepdog-${task.kind}-${task.key.toLowerCase()}`,
        onClick: () => {
          if (currentRepo(state).toLowerCase() === repo.toLowerCase()) actions.select(number)
        },
      })
    }
  }

  async function startTask(
    kind: 'review' | 'classify',
    repo: string,
    number: number,
    start: (client: ApiClient) => Promise<RunningTask>,
  ) {
    const k = key(repo, number)
    const running = kind === 'review' ? isReviewing : isClassifying
    if (running(state, repo, number)) return
    const setError = (error: string | null) =>
      dispatch({ type: kind === 'review' ? 'reviewError' : 'classificationError', key: k, error })
    const pending = kind === 'review' ? 'startingReviews' : 'startingClassifications'
    const generation = state.generation
    setError(null)
    dispatch({ type: 'pending', set: pending, key: k, on: true })
    const version = tasksVersion
    const own = `${kind} ${k}`
    ownStarts.add(own)
    try {
      const task = await checked(start)
      // A tasks event after the start has the current list, which can be without this task if it already ended.
      if (version === tasksVersion) dispatch({ type: 'taskStarted', task })
    } catch (error) {
      ownStarts.delete(own)
      if (!(error instanceof Cancelled)) setError(message(error))
    } finally {
      if (generation === state.generation) dispatch({ type: 'pending', set: pending, key: k, on: false })
    }
  }

  const actions = {
    // Reads the session cookie through GET /api/me.
    async start() {
      try {
        open(await api.me())
      } catch (error) {
        const unauthorized = error instanceof ApiError && error.kind === 'unauthorized'
        dispatch({ type: 'signedOut', message: unauthorized ? null : message(error) })
      }
    },

    // Returns true after a sign-in, so that the form can clear the token field.
    async signIn(githubToken: string): Promise<boolean> {
      // A late answer to a sign-out deletes the cookie, also the cookie of a newer sign-in.
      await signingOut
      set({ signInError: null })
      try {
        open((await api.signIn(githubToken.trim())).login)
        return true
      } catch (error) {
        set({ signInError: message(error) })
        return false
      }
    },

    // The local session ends first, so that an unreachable server does not delay it.
    async signOut() {
      if (state.auth !== 'signedIn') return
      endSession(null)
      signingOut = api.signOut().catch(() => {})
      await signingOut
    },

    async signOutEverywhere() {
      if (state.auth !== 'signedIn') return
      set({ settingsError: null })
      // The server ends the streams of the user, and a stopped stream does not connect again with the ended session.
      stream?.close()
      try {
        await checked((client) => client.signOutEverywhere())
        endSession(null)
      } catch (error) {
        if (error instanceof Cancelled) return
        set({ settingsError: message(error) })
        startStream()
      }
    },

    select(number: number | null) {
      set({ selection: number })
    },

    async loadSettings() {
      const settings = await call('settingsError', (client) => client.settings())
      if (settings) set({ settings })
    },

    async selectRepo(name: string) {
      const settings = await call('error', (client) => client.updateSettings({ repo: name }))
      if (settings) set({ settings })
    },

    async saveOpenRouterKey(openRouterKey: string): Promise<boolean> {
      const settings = await call('settingsError', (client) => client.updateSettings({ openRouterKey }))
      if (!settings) return false
      set({ settings, models: null, modelsError: null })
      return true
    },

    // Returns true after a save, so that the field can show it. An empty text deletes the global rules.
    async saveGlobalRules(globalRules: string): Promise<boolean> {
      const settings = await call('settingsError', (client) => client.updateSettings({ globalRules }))
      if (!settings) return false
      set({ settings })
      return true
    },

    // null when the result no longer matters. Throws an ApiError that the settings show.
    async globalRulesTemplate(): Promise<string | null> {
      try {
        return await checked((client) => client.globalRulesTemplate())
      } catch (error) {
        if (error instanceof Cancelled) return null
        throw error
      }
    },

    async selectModel(model: string) {
      const settings = await call('settingsError', (client) => client.updateSettings({ model }))
      if (settings) set({ settings })
    },

    async loadModels() {
      if (state.models !== null) return
      set({ modelsError: null })
      try {
        set({ models: await checked((client) => client.models()) })
      } catch (error) {
        if (!(error instanceof Cancelled)) set({ modelsError: message(error) })
      }
    },

    // The sidebar and the picker both start a load, and a late failure must not hide a list that loaded.
    async loadRepositories() {
      if (state.repositories !== null || loadingRepositories === state.generation) return
      const generation = state.generation
      loadingRepositories = generation
      set({ repositoriesError: null })
      try {
        set({ repositories: await checked((client) => client.repos()) })
      } catch (error) {
        if (!(error instanceof Cancelled)) set({ repositoriesError: message(error) })
      } finally {
        if (loadingRepositories === generation) loadingRepositories = null
      }
    },

    // The star changes at once, and goes back when the server refuses. A second click before the answer sends nothing.
    async toggleStar(repo: string) {
      const k = repo.toLowerCase()
      if (!state.settings || state.starring.has(k)) return
      const on = !isStarred(state, k)
      const generation = state.generation
      set({ starError: null })
      dispatch({ type: 'pending', set: 'starring', key: k, on: true })
      dispatch({ type: 'star', repo: k, on })
      try {
        const settings = await checked((client) => (on ? client.star(k) : client.unstar(k)))
        // The answer can miss a star that another running request changes.
        if (state.starring.size === 1 && state.settings) set({ settings: { ...state.settings, starred: settings.starred } })
      } catch (error) {
        if (error instanceof Cancelled) return
        dispatch({ type: 'star', repo: k, on: !on })
        set({ starError: message(error) })
      } finally {
        if (generation === state.generation) dispatch({ type: 'pending', set: 'starring', key: k, on: false })
      }
    },

    // A second click before the next requests event sends nothing.
    async refresh() {
      if (state.refreshing) return
      set({ refreshing: true, refreshError: null })
      try {
        await checked((client) => client.refresh())
      } catch (error) {
        if (!(error instanceof Cancelled)) set({ refreshing: false, refreshError: message(error) })
      }
    },

    // A running change of a switch keeps its value, and a late load of another repository changes nothing.
    async loadRepoSettings(repo: string) {
      const k = repo.toLowerCase()
      loadingRepoSettings = k
      set({ repoSettingsError: null, repoSettings: state.repoSettings?.repo === k ? state.repoSettings : null })
      try {
        const loaded = await checked((client) => client.repoSettings(k))
        if (loadingRepoSettings === k && state.savingPreferences.size === 0) set({ repoSettings: loaded })
      } catch (error) {
        if (!(error instanceof Cancelled) && loadingRepoSettings === k) set({ repoSettingsError: message(error) })
      }
    },

    // The switch changes at once, and goes back when the server refuses. A second change before the answer sends nothing.
    async setRepoPreference(name: Preference, on: boolean) {
      const current = state.repoSettings
      if (!current || state.savingPreferences.has(name)) return
      const generation = state.generation
      set({ repoSettings: { ...current, [name]: on }, repoSettingsError: null })
      dispatch({ type: 'pending', set: 'savingPreferences', key: name, on: true })
      try {
        await checked((client) => client.updateRepoSettings(current.repo, { [name]: on }))
        // The repository picker sorts and marks the repositories with auto classify or auto review by these lists.
        if (name !== 'autoRefresh' && state.settings) {
          const list = repoLists[name]
          const others = state.settings[list].filter((repo) => repo !== current.repo)
          set({ settings: { ...state.settings, [list]: on ? [...others, current.repo].sort() : others } })
        }
      } catch (error) {
        if (error instanceof Cancelled) return
        if (state.repoSettings?.repo === current.repo) {
          set({ repoSettings: { ...state.repoSettings, [name]: !on }, repoSettingsError: message(error) })
        }
      } finally {
        if (generation === state.generation) dispatch({ type: 'pending', set: 'savingPreferences', key: name, on: false })
      }
    },

    // null when the result no longer matters. Throws an ApiError that the detail panel shows.
    async pullRequest(repo: string, number: number): Promise<PullRequest | null> {
      try {
        return await checked((client) => client.pullRequest(repo, number))
      } catch (error) {
        if (error instanceof Cancelled) return null
        throw error
      }
    },

    // Works as pullRequest does.
    async pullDiff(repo: string, number: number): Promise<PullDiff | null> {
      try {
        return await checked((client) => client.pullDiff(repo, number))
      } catch (error) {
        if (error instanceof Cancelled) return null
        throw error
      }
    },

    // A review event can arrive while the request runs, so an older review never replaces a newer one, and a new error stays.
    async loadReview(repo: string, number: number) {
      const k = key(repo, number)
      const previousError = state.reviewErrors[k]
      try {
        const review = await checked((client) => client.review(repo, number))
        dispatch({ type: 'reviewLoaded', key: k, review, previousError })
      } catch (error) {
        if (!(error instanceof Cancelled)) dispatch({ type: 'reviewError', key: k, error: message(error) })
      }
    },

    startReview(repo: string, number: number) {
      return startTask('review', repo, number, (client) => client.startReview(repo, number))
    },

    // Works as loadReview does.
    async loadClassification(repo: string, number: number) {
      const k = key(repo, number)
      const previousError = state.classificationErrors[k]
      try {
        const classification = await checked((client) => client.classification(repo, number))
        dispatch({ type: 'classificationLoaded', key: k, classification, previousError })
      } catch (error) {
        if (!(error instanceof Cancelled)) dispatch({ type: 'classificationError', key: k, error: message(error) })
      }
    },

    async loadClassifications(repo: string) {
      try {
        const list = await checked((client) => client.classifications(repo))
        dispatch({ type: 'classificationsLoaded', list })
      } catch (error) {
        if (error instanceof Cancelled) return
        listsRepo = null
        // A 409 comes for a repository without rules, before the poll shows the setup.
        if (!(error instanceof ApiError && error.status === 409)) set({ error: message(error) })
      }
    },

    // The list marks the reviewed PRs, so it needs the stored reviews of the repository before a PR opens.
    async loadReviews(repo: string) {
      try {
        const list = await checked((client) => client.reviews(repo))
        dispatch({ type: 'reviewsLoaded', list })
      } catch (error) {
        if (error instanceof Cancelled) return
        listsRepo = null
        if (!(error instanceof ApiError && error.status === 409)) set({ error: message(error) })
      }
    },

    startClassification(repo: string, number: number) {
      return startTask('classify', repo, number, (client) => client.startClassification(repo, number))
    },

    // Runs one PR after another with the head commit that the user confirmed, and goes on after a failure.
    async approveAndMerge(rows: Row[]) {
      const repo = currentRepo(state)
      const generation = state.generation
      set({ error: null })
      const failures: string[] = []
      for (const row of rows) {
        const k = key(repo, row.number)
        if (state.merging.has(k)) continue
        dispatch({ type: 'pending', set: 'merging', key: k, on: true })
        try {
          await checked((client) => client.approveAndMerge(repo, row.number, row.headSha))
        } catch (error) {
          if (error instanceof Cancelled) return
          failures.push(`#${row.number}: ${message(error)}`)
        } finally {
          if (generation === state.generation) dispatch({ type: 'pending', set: 'merging', key: k, on: false })
        }
      }
      if (failures.length > 0) set({ error: ['Approve and merge failed.', ...failures].join('\n') })
    },

    // null when the result no longer matters, or while the review is being posted. Throws an ApiError that the review card shows.
    async postReview(repo: string, number: number): Promise<Review | null> {
      const k = key(repo, number)
      if (state.postingReviews.has(k)) return null
      const generation = state.generation
      dispatch({ type: 'pending', set: 'postingReviews', key: k, on: true })
      try {
        const posted = await checked((client) => client.postReview(repo, number))
        dispatch({ type: 'reviewLoaded', key: k, review: posted, previousError: state.reviewErrors[k] })
        return posted
      } catch (error) {
        if (error instanceof Cancelled) return null
        throw error
      } finally {
        if (generation === state.generation) dispatch({ type: 'pending', set: 'postingReviews', key: k, on: false })
      }
    },

    // null when the result no longer matters. Throws an ApiError that the setup dialog shows.
    async setupTemplate(repo: string): Promise<string | null> {
      try {
        return await checked((client) => client.setupTemplate(repo))
      } catch (error) {
        if (error instanceof Cancelled) return null
        throw error
      }
    },

    // Works as setupTemplate does, and also gives null while a setup of the repository runs, so that a second click sends nothing.
    async setUp(repo: string, content: string): Promise<PullLink | null> {
      if (isSettingUp(state, repo)) return null
      const k = repo.toLowerCase()
      const generation = state.generation
      dispatch({ type: 'pending', set: 'settingUp', key: k, on: true })
      try {
        return await checked((client) => client.setup(repo, content))
      } catch (error) {
        if (error instanceof Cancelled) return null
        throw error
      } finally {
        if (generation === state.generation) dispatch({ type: 'pending', set: 'settingUp', key: k, on: false })
      }
    },

    async cancelTask(id: string) {
      set({ error: null })
      try {
        await checked((client) => client.cancelTask(id))
      } catch (error) {
        if (error instanceof Cancelled) return
        if (!(error instanceof ApiError && error.status === 404)) {
          set({ error: message(error) })
          return
        }
      }
      dispatch({ type: 'taskCancelled', id })
    },
  }

  return {
    getState,
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    dispatch,
    actions,
  }
}
