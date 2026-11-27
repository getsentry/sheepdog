import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeAll, expect, it } from 'vitest'
import { App } from './App'
import { StoreProvider } from './context'
import { createStore } from './store'
import { failure, fakeEventSources, fakeFetch, Gate, reply, settle, type Seen } from './test/fakes'
import { laneRow, requests } from './test/rows'
import { approveAndMergeAction, type PullRequest, type Requests, type Setup } from './types'

// jsdom has no modal dialogs.
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
    this.setAttribute('open', '')
  }
  HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
    this.removeAttribute('open')
    this.dispatchEvent(new Event('close'))
  }
  // jsdom has no pointer capture and does not scroll.
  HTMLElement.prototype.setPointerCapture = () => {}
  HTMLElement.prototype.scrollIntoView = function (this: HTMLElement) {
    scrolledTo.push(this)
  }
})

const scrolledTo: HTMLElement[] = []

afterEach(() => {
  scrolledTo.length = 0
  cleanup()
  localStorage.clear()
})

const pullRequest: PullRequest = {
  title: 'PR 2',
  body: '<!-- template -->\nSee [the docs](https://example.com/docs).\n\n<b>bold</b>',
  headRefName: 'bump',
  baseRefName: 'main',
  headRefOid: 'sha2',
  changedFiles: 2,
  createdAt: '2027-01-14T08:00:00Z',
  updatedAt: '2027-01-15T08:00:00Z',
  reviewDecision: 'REVIEW_REQUIRED',
  labels: ['dependencies'],
  reviewRequests: ['alwx'],
  latestReviews: [],
  checks: { passed: 3, failed: 1, pending: 0, skipped: 0 },
}

const bigFile = Array.from({ length: 401 }, (_, index) => `+line ${index + 1}`)
const prDiff = [
  'diff --git a/package.json b/package.json',
  '--- a/package.json',
  '+++ b/package.json',
  '@@ -3,3 +3,3 @@',
  '   "name": "app",',
  '-  "expo": "57.0.1"',
  '+  "expo": "57.2.0"',
  ' }',
  'diff --git a/package-lock.json b/package-lock.json',
  '--- a/package-lock.json',
  '+++ b/package-lock.json',
  '@@ -0,0 +1,401 @@',
  ...bigFile,
  '',
].join('\n')

const rows = [
  laneRow(2, 'safe', approveAndMergeAction, { source: 'ci', score: 1, reason: 'Lockfile only', model: 'm', headSha: 'sha2' }),
  laneRow(3, 'safe', approveAndMergeAction),
  laneRow(4, null),
]

async function signedIn(handler: (seen: Seen) => ReturnType<Parameters<typeof fakeFetch>[0]> | undefined = () => undefined) {
  let session = false
  const server = fakeFetch((seen) => {
    const custom = handler(seen)
    if (custom) return custom
    const route = `${seen.method} ${seen.path}`
    if (route === 'POST /api/session') {
      session = true
      return reply(200, { login: 'alwx', sessionToken: 'session-token' })
    }
    if (!session) return failure(401, 'Sign in first')
    switch (route) {
      case 'GET /api/me':
        return reply(200, { login: 'alwx' })
      case 'GET /api/settings':
        return reply(200, { repo: 'acme/app', model: 'a/model', hasOpenRouterKey: true, starred: [], autoClassifyRepos: [], autoReviewRepos: [] })
      case 'GET /api/repos':
        return reply(200, ['acme/app'])
      case 'GET /api/models':
        return reply(200, ['a/model', 'b/model'])
      case 'GET /api/repos/acme/app/classifications':
      case 'GET /api/repos/acme/app/reviews':
        return reply(200, [])
      case 'GET /api/repos/acme/app/pulls/2':
        return reply(200, pullRequest)
      case 'GET /api/repos/acme/app/pulls/2/diff':
        return reply(200, { diff: prDiff, truncated: false })
      case 'POST /api/repos/acme/app/pulls/2/approve-and-merge':
        return reply(200, { result: 'merged' })
      case 'POST /api/repos/acme/app/pulls/2/classification':
        return reply(200, { id: 'T1', kind: 'classify', key: 'acme/app#2', title: 'Classify #2', subtitle: 'acme/app', startedAt: '2027-01-15T08:00:00Z' })
    }
    return seen.method === 'DELETE' ? reply(204) : failure(404, 'Not found')
  })
  const sources = fakeEventSources()
  const store = createStore({ fetch: server.fetch, EventSource: sources.EventSource })
  await act(() => store.actions.start())
  render(
    <StoreProvider store={store}>
      <App />
    </StoreProvider>,
  )
  fireEvent.change(screen.getByLabelText('GitHub token'), { target: { value: 'ghp_test' } })
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Sign in' })))
  await act(async () => {
    sources.last().open()
    sources.last().emit('requests', { ...requests(rows, ['safe', 'docs']), fetchedAt: '2027-01-15T08:00:00Z' })
    await settle()
  })
  const sent = (method: string, path: string) => server.seen.filter((seen) => seen.method === method && seen.path === path)
  return { server, sources, sent }
}

async function open(number: number) {
  await act(async () => {
    fireEvent.click(screen.getByText(`PR ${number}`).closest('button')!)
    await settle()
  })
}

it('signs in and groups the PRs by lane without storing the session', async () => {
  await signedIn()

  expect(screen.queryByLabelText('GitHub token')).toBeNull()
  const safe = screen.getByRole('region', { name: 'safe' })
  expect(within(safe).getByRole('heading').textContent).toBe('safe2Approve and merge (2)')
  expect(within(safe).getAllByRole('listitem').map((item) => item.textContent?.slice(0, 2))).toEqual(['#2', '#3'])
  expect(within(screen.getByRole('region', { name: 'manual' })).getAllByRole('listitem')).toHaveLength(1)
  expect(screen.queryByRole('region', { name: 'docs' })).toBeNull()
  expect(screen.getByTitle('Danger 1/10 from CI').textContent).toBe('1')
  expect(localStorage.length + sessionStorage.length).toBe(0)
  expect(document.cookie).toBe('')
})

it('shows the detail of a PR with its title, facts, and body', async () => {
  await signedIn()
  await open(2)

  expect(document.title).toBe('PR 2')
  expect(screen.getByText('main ← bump')).toBeTruthy()
  expect(screen.getByText('3 passed, 1 failed').className).toBe('text-red')
  expect(screen.getByText('Review required')).toBeTruthy()
  expect(within(screen.getByRole('region', { name: 'Classification' })).getByText('From CI, m')).toBeTruthy()
  const link = screen.getByRole('link', { name: 'the docs' })
  expect(link.getAttribute('target')).toBe('_blank')
  expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  expect(screen.getByText('<b>bold</b>')).toBeTruthy()
  expect(screen.queryByText(/template/)).toBeNull()
})

it('sends approve and merge only after the confirmation, and once for a double click', async () => {
  const { sent } = await signedIn()
  await open(2)
  const path = '/api/repos/acme/app/pulls/2/approve-and-merge'

  fireEvent.click(screen.getByRole('button', { name: 'Approve and merge' }))
  const dialog = screen.getByRole('dialog', { name: 'Approve and merge' })
  expect(within(dialog).getByRole('listitem').textContent).toBe('#2 PR 2')
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(sent('POST', path)).toHaveLength(0)

  fireEvent.click(screen.getByRole('button', { name: 'Approve and merge' }))
  const confirm = within(screen.getByRole('dialog')).getByRole('button', { name: 'Approve and merge' })
  await act(async () => {
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    await settle()
  })
  expect(sent('POST', path).map((seen) => seen.body)).toEqual([{ headSha: 'sha2' }])
})

it('sends one classification for a double click on Classify', async () => {
  const { sent } = await signedIn()
  await open(2)

  const classify = screen.getByRole('button', { name: 'Classify' })
  await act(async () => {
    fireEvent.click(classify)
    fireEvent.click(classify)
    await settle()
  })
  expect(sent('POST', '/api/repos/acme/app/pulls/2/classification')).toHaveLength(1)
  expect(screen.getByRole('region', { name: 'Running tasks' }).textContent).toContain('Classify #2')
})

it('picks a model in the settings and signs out', async () => {
  const { sent } = await signedIn()

  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Settings' })))
  const settings = screen.getByRole('dialog', { name: 'Settings' })
  expect(within(settings).getByLabelText<HTMLInputElement>('OpenRouter API key').placeholder).toBe('Saved on the server')
  await act(async () => {
    const modelButton = within(settings).getByRole('button', { name: /a\/model/ })
    expect(within(settings).getByText('Default model')).toBeTruthy()
    expect(document.getElementById(modelButton.getAttribute('aria-describedby')!)?.textContent).toContain(
      'Sheepdog uses this model for classifications and reviews when .github/sheepdog.yml of the repository',
    )
    fireEvent.click(modelButton)
    await settle()
  })
  const models = screen.getByRole('dialog', { name: 'Models' })
  await act(async () => {
    fireEvent.click(within(models).getByRole('button', { name: 'b/model' }))
    await settle()
  })
  expect(sent('PUT', '/api/settings').map((seen) => seen.body)).toEqual([{ model: 'b/model' }])
  expect(screen.queryByRole('dialog', { name: 'Models' })).toBeNull()
  expect(screen.getByRole('dialog', { name: 'Settings' })).toBeTruthy()

  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Sign out' })))
  expect(screen.getByLabelText<HTMLInputElement>('GitHub token').value).toBe('')
})

const template = 'version: 1\nlanes: {}\n'
const setupPath = '/api/repos/acme/new/setup'

function withoutRules(setup: Setup): Requests {
  return { repo: 'acme/new', fetchedAt: '2027-01-15T08:01:00Z', rows: [], lanes: [], setup }
}

async function emit(sources: ReturnType<typeof fakeEventSources>, data: Requests) {
  await act(async () => {
    sources.last().emit('requests', data)
    await settle()
  })
}

it('shows the setup warning instead of the list, closes the detail, and loads no classifications', async () => {
  const { sources, sent } = await signedIn()
  await open(2)
  expect(screen.getByRole('heading', { name: 'PR 2' })).toBeTruthy()

  await emit(sources, withoutRules({ defaultBranch: 'main', pullRequest: null }))
  const warning = screen.getByRole('region', { name: 'Setup' })
  expect(warning.textContent).toContain(
    'acme/new has no .github/sheepdog.yml on its default branch main. Sheepdog works only with repositories that have this file, or with your global rules.',
  )
  expect(within(warning).getByRole('button', { name: 'Create sheepdog.yml' })).toBeTruthy()
  fireEvent.click(within(warning).getByRole('button', { name: 'Set global rules' }))
  expect(screen.getByRole('dialog', { name: 'Settings' })).toBeTruthy()
  fireEvent.click(within(screen.getByRole('dialog', { name: 'Settings' })).getByRole('button', { name: 'Close' }))
  expect(screen.queryByRole('region', { name: 'safe' })).toBeNull()
  expect(screen.queryByText('No review requests')).toBeNull()
  expect(screen.getByRole('main').textContent).toBe('')
  expect(document.title).toBe('Sheepdog')
  expect(screen.queryByRole('alert')).toBeNull()
  expect(sent('GET', '/api/repos/acme/new/classifications')).toHaveLength(0)
  expect(sent('GET', '/api/repos/acme/new/setup/template')).toHaveLength(0)

  await emit(sources, withoutRules({ defaultBranch: 'main', pullRequest: { number: 7, url: 'https://github.com/acme/new/pull/7' } }))
  const waiting = screen.getByRole('region', { name: 'Setup' })
  expect(waiting.textContent).toContain('Sheepdog waits for the merge of pull request #7, which adds the file.')
  const link = within(waiting).getByRole('link', { name: 'pull request #7' })
  expect(link.getAttribute('href')).toBe('https://github.com/acme/new/pull/7')
  expect(link.getAttribute('target')).toBe('_blank')
  expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  expect(within(waiting).getAllByRole('button').map((button) => button.textContent)).toEqual(['Set global rules'])
  expect(sent('GET', '/api/repos/acme/new/classifications')).toHaveLength(0)
})

it('opens the setup pull request only after the click, once for a double click, and shows its link', async () => {
  const gate = new Gate()
  const { sources, sent } = await signedIn((seen) => {
    if (seen.path === `${setupPath}/template`) return reply(200, { content: template })
    if (seen.path === setupPath) return gate.wait().then(() => reply(201, { number: 12, url: 'https://github.com/acme/new/pull/12' }))
  })
  await emit(sources, withoutRules({ defaultBranch: 'main', pullRequest: null }))

  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Create sheepdog.yml' }))
    await settle()
  })
  let dialog = screen.getByRole('dialog', { name: 'Create sheepdog.yml' })
  expect(within(dialog).getByLabelText<HTMLTextAreaElement>('sheepdog.yml').value).toBe(template)
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(sent('POST', setupPath)).toHaveLength(0)

  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Create sheepdog.yml' }))
    await settle()
  })
  dialog = screen.getByRole('dialog', { name: 'Create sheepdog.yml' })
  fireEvent.change(within(dialog).getByLabelText('sheepdog.yml'), { target: { value: `${template}# edited\n` } })
  expect(sent('POST', setupPath)).toHaveLength(0)
  const openButton = within(dialog).getByRole('button', { name: 'Open pull request' })
  await act(async () => {
    fireEvent.click(openButton)
    fireEvent.click(openButton)
    await settle()
  })
  expect(sent('GET', `${setupPath}/template`)).toHaveLength(2)
  expect(sent('POST', setupPath).map((seen) => seen.body)).toEqual([{ content: `${template}# edited\n` }])
  expect(within(dialog).getByRole('button', { name: /Opening/ }).hasAttribute('disabled')).toBe(true)

  await act(async () => {
    gate.open()
    await settle()
  })
  const link = within(dialog).getByRole('link', { name: 'pull request #12' })
  expect(link.getAttribute('href')).toBe('https://github.com/acme/new/pull/12')
  expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  expect(sent('POST', setupPath)).toHaveLength(1)

  await emit(sources, withoutRules({ defaultBranch: 'main', pullRequest: { number: 12, url: 'https://github.com/acme/new/pull/12' } }))
  expect(screen.getByRole('region', { name: 'Setup' }).textContent).toContain('Sheepdog waits for the merge of pull request #12')
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' }))
  expect(screen.queryByRole('dialog')).toBeNull()
})

it('shows the errors of the server in the setup dialog', async () => {
  const branchExists = 'The branch sheepdog/setup already exists. Its pull request is https://github.com/acme/new/pull/3'
  let templateFails = true
  const { sources, sent } = await signedIn((seen) => {
    if (seen.path === `${setupPath}/template`) return templateFails ? failure(502, 'GitHub: Bad credentials') : reply(200, { content: template })
    if (seen.path === setupPath) return failure(409, branchExists)
  })
  await emit(sources, withoutRules({ defaultBranch: 'main', pullRequest: null }))

  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Create sheepdog.yml' }))
    await settle()
  })
  let dialog = screen.getByRole('dialog', { name: 'Create sheepdog.yml' })
  expect(within(dialog).getByText('GitHub: Bad credentials')).toBeTruthy()
  expect(within(dialog).getByRole('button', { name: 'Open pull request' }).hasAttribute('disabled')).toBe(true)
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))

  templateFails = false
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Create sheepdog.yml' }))
    await settle()
  })
  dialog = screen.getByRole('dialog', { name: 'Create sheepdog.yml' })
  await act(async () => {
    fireEvent.click(within(dialog).getByRole('button', { name: 'Open pull request' }))
    await settle()
  })
  expect(within(dialog).getByText(branchExists)).toBeTruthy()
  expect(within(dialog).getByRole('button', { name: 'Open pull request' }).hasAttribute('disabled')).toBe(false)
  expect(within(dialog).queryByRole('link')).toBeNull()
  expect(screen.queryByRole('alert')).toBeNull()
  expect(sent('POST', setupPath)).toHaveLength(1)
})

it('shows the starred repositories first, also while searching, and a click on a star selects nothing', async () => {
  let starred = ['acme/tools']
  const { sent } = await signedIn((seen) => {
    if (seen.path === '/api/repos') return reply(200, ['acme/app', 'Acme/Tools', 'acme/web', 'other/tooling'])
    if (seen.path === '/api/settings') return reply(200, { repo: 'acme/app', model: 'a/model', hasOpenRouterKey: true, starred, autoClassifyRepos: [], autoReviewRepos: [] })
    if (seen.path === '/api/repos/acme/web/star') {
      starred = seen.method === 'PUT' ? ['acme/tools', 'acme/web'] : ['acme/tools']
      return reply(200, { repo: 'acme/app', model: 'a/model', hasOpenRouterKey: true, starred, autoClassifyRepos: [], autoReviewRepos: [] })
    }
  })
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /acme\/app/ }))
    await settle()
  })
  const picker = screen.getByRole('dialog', { name: 'Repositories' })
  const list = () =>
    within(picker)
      .getAllByRole('listitem')
      .map((item) => {
        const [name, star] = within(item).getAllByRole('button')
        return `${name.textContent} ${star.getAttribute('aria-label')}`
      })
  expect(list()).toEqual(['Acme/Tools Remove star', 'acme/app Star', 'acme/web Star', 'other/tooling Star'])

  const web = within(picker).getAllByRole('listitem')[2]
  await act(async () => {
    fireEvent.click(within(web).getByRole('button', { name: 'Star' }))
    await settle()
  })
  expect(list()).toEqual(['Acme/Tools Remove star', 'acme/web Remove star', 'acme/app Star', 'other/tooling Star'])
  expect(screen.getByRole('dialog', { name: 'Repositories' })).toBe(picker)
  expect(sent('PUT', '/api/settings')).toHaveLength(0)
  expect(sent('PUT', '/api/repos/acme/web/star')).toHaveLength(1)

  fireEvent.change(within(picker).getByRole('searchbox'), { target: { value: 'tool' } })
  expect(list()).toEqual(['Acme/Tools Remove star', 'other/tooling Star'])
  fireEvent.change(within(picker).getByRole('searchbox'), { target: { value: 'acme/' } })
  expect(list()).toEqual(['Acme/Tools Remove star', 'acme/web Remove star', 'acme/app Star'])
})

it('shows a refused star in the picker, and puts it back', async () => {
  const { sent } = await signedIn((seen) => {
    if (seen.path === '/api/repos') return reply(200, ['acme/app', 'acme/web'])
    if (seen.path === '/api/repos/acme/web/star') return failure(409, 'You can star at most 200 repositories. Remove a star first')
  })
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /acme\/app/ }))
    await settle()
  })
  const picker = screen.getByRole('dialog', { name: 'Repositories' })
  const web = within(picker).getAllByRole('listitem')[1]
  await act(async () => {
    fireEvent.click(within(web).getByRole('button', { name: 'Star' }))
    await settle()
  })
  expect(within(picker).getByRole('alert').textContent).toBe('You can star at most 200 repositories. Remove a star first')
  expect(within(picker).getAllByRole('button', { name: 'Star' })).toHaveLength(2)
  expect(sent('PUT', '/api/repos/acme/web/star')).toHaveLength(1)
})

it('shows the update time in the footer, and a spinner on refresh until the next requests event', async () => {
  const { sources, sent } = await signedIn((seen) => {
    if (seen.method === 'POST' && seen.path === '/api/review-requests/refresh') return reply(202)
  })
  const footer = screen.getByRole('complementary').querySelector('footer')!
  const updates = within(footer).getByRole('region', { name: 'Updates' })
  const time = new Date('2027-01-15T08:00:00Z').toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })
  expect(updates.textContent).toBe(`Updated at ${time} Repository settings`)
  expect(screen.getAllByText(/Updated at/)).toHaveLength(1)
  expect(footer.lastElementChild?.textContent).toBe(' Settings')
  expect(within(footer).queryByRole('switch')).toBeNull()

  const refresh = within(updates).getByRole('button', { name: 'Refresh' })
  await act(async () => {
    fireEvent.click(refresh)
    fireEvent.click(refresh)
    await settle()
  })
  expect(refresh.hasAttribute('disabled')).toBe(true)
  expect(within(refresh).getByRole('status', { name: 'Refreshing' })).toBeTruthy()
  expect(sent('POST', '/api/review-requests/refresh')).toHaveLength(1)

  await emit(sources, { ...requests(rows, ['safe', 'docs']), fetchedAt: null })
  expect(refresh.hasAttribute('disabled')).toBe(false)
  expect(within(refresh).queryByRole('status')).toBeNull()
  expect(updates.textContent).toBe('Not updated yet Repository settings')
})

it('stops the refresh spinner and shows the error when the refresh fails', async () => {
  await signedIn((seen) => {
    if (seen.path === '/api/review-requests/refresh') return failure(409, 'Select a repository first')
  })
  const updates = screen.getByRole('region', { name: 'Updates' })
  await act(async () => {
    fireEvent.click(within(updates).getByRole('button', { name: 'Refresh' }))
    await settle()
  })
  expect(within(updates).getByRole('alert').textContent).toBe('Select a repository first')
  expect(within(updates).getByRole('button', { name: 'Refresh' }).hasAttribute('disabled')).toBe(false)
})

const repoSettingsPath = '/api/repos/acme/app/settings'

async function openRepoSettings() {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Repository settings' }))
    await settle()
  })
  return screen.getByRole('dialog', { name: 'Repository settings' })
}

it('opens the repository settings with refresh, classify, and review', async () => {
  const gate = new Gate()
  let fails = false
  const { sent } = await signedIn((seen) => {
    if (seen.path !== repoSettingsPath) return
    if (seen.method === 'GET') return reply(200, { repo: 'acme/app', autoRefresh: true, autoClassify: false, autoReview: false })
    return gate.wait().then(() =>
      fails
        ? failure(500, 'Internal server error')
        : reply(200, { repo: 'acme/app', autoRefresh: true, autoClassify: true, autoReview: true }),
    )
  })
  const dialog = await openRepoSettings()
  expect(within(dialog).getByText('These settings apply only to acme/app.')).toBeTruthy()
  const section = (name: string) => within(dialog).getByRole('region', { name })
  expect(within(dialog).getAllByRole('region').map((region) => region.getAttribute('aria-label'))).toEqual(['Refresh', 'Classify', 'Review'])
  const refresh = within(section('Refresh')).getByRole<HTMLInputElement>('switch', { name: 'Auto-refresh' })
  const classify = within(section('Classify')).getByRole<HTMLInputElement>('switch', { name: 'Auto-classify' })
  const review = within(section('Review')).getByRole<HTMLInputElement>('switch', { name: 'Auto-review' })
  expect([refresh.checked, classify.checked, review.checked, review.disabled]).toEqual([true, false, false, false])
  expect(section('Classify').textContent).toContain('It uses tokens, and you pay for them.')
  expect(section('Review').textContent).toContain('except drafts, at most two at a time')
  expect(section('Review').textContent).toContain('It uses tokens, and you pay for them.')
  expect(within(dialog).queryByText(/needs an OpenRouter API key/)).toBeNull()

  await act(async () => {
    fireEvent.click(classify)
    await settle()
  })
  expect(classify.checked).toBe(true)
  await act(async () => {
    fireEvent.click(review)
    await settle()
  })
  expect(review.checked).toBe(true)
  await act(async () => {
    gate.open()
    await settle()
  })
  expect(classify.checked).toBe(true)
  expect(review.checked).toBe(true)

  fails = true
  await act(async () => {
    fireEvent.click(refresh)
    await settle()
  })
  expect(refresh.checked).toBe(true)
  expect(within(dialog).getByRole('alert').textContent).toBe('Internal server error')
  expect(sent('PUT', repoSettingsPath).map((seen) => seen.body)).toEqual([{ autoClassify: true }, { autoReview: true }, { autoRefresh: false }])
  expect(sent('PUT', '/api/settings')).toHaveLength(0)

  fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
  expect(screen.queryByRole('dialog')).toBeNull()
})

it('links the configuration in the repository settings to the GitHub editor on the default branch', async () => {
  const { sources } = await signedIn((seen) => {
    if (seen.path === repoSettingsPath) return reply(200, { repo: 'acme/app', autoRefresh: true, autoClassify: false })
  })
  const dialog = await openRepoSettings()
  expect(within(dialog).queryByRole('link', { name: 'Edit configuration' })).toBeNull()

  await emit(sources, { ...requests(rows, ['safe']), fetchedAt: '2027-01-15T08:00:00Z', defaultBranch: 'release/2.x' })
  const link = within(dialog).getByRole('link', { name: 'Edit configuration' })
  expect(link.getAttribute('href')).toBe('https://github.com/acme/app/edit/release/2.x/.github/sheepdog.yml')
  expect(link.getAttribute('target')).toBe('_blank')
  expect(link.getAttribute('rel')).toBe('noopener noreferrer')

  await emit(sources, { ...withoutRules({ defaultBranch: 'main', pullRequest: null }), repo: 'acme/app', defaultBranch: 'main' })
  expect(within(dialog).queryByRole('link', { name: 'Edit configuration' })).toBeNull()
})

it('says in the repository settings when auto-classify misses the key or the model', async () => {
  await signedIn((seen) => {
    if (seen.path === '/api/settings') return reply(200, { repo: 'acme/app', model: '', hasOpenRouterKey: false, starred: [], autoClassifyRepos: [], autoReviewRepos: [] })
    if (seen.path === repoSettingsPath) return reply(200, { repo: 'acme/app', autoRefresh: false, autoClassify: true })
  })
  const dialog = await openRepoSettings()
  expect(within(dialog).getByRole<HTMLInputElement>('switch', { name: 'Auto-refresh' }).checked).toBe(false)
  expect(within(dialog).getByText('Auto-classify needs an OpenRouter API key and a default model in Settings.')).toBeTruthy()
})

it('shows a failed load of the repository settings', async () => {
  await signedIn((seen) => {
    if (seen.path === repoSettingsPath) return failure(500, 'Internal server error')
  })
  const dialog = await openRepoSettings()
  expect(within(dialog).queryByRole('switch')).toBeNull()
  expect(within(dialog).queryByRole('status')).toBeNull()
  expect(within(dialog).getByRole('alert').textContent).toBe('Internal server error')
})

it('shows the list with a short warning for a repository with the global rules', async () => {
  const { sources, sent } = await signedIn((seen) => {
    if (seen.path === '/api/repos/acme/new/classifications' || seen.path === '/api/repos/acme/new/reviews') return reply(200, [])
    if (seen.path === '/api/repos/acme/new/pulls/2') return reply(200, pullRequest)
  })
  const global = (setup: Setup): Requests => ({
    ...requests(rows, ['safe']),
    repo: 'acme/new',
    fetchedAt: '2027-01-15T08:01:00Z',
    setup,
    globalRules: true,
  })
  await emit(sources, global({ defaultBranch: 'main', pullRequest: null }))
  const warning = screen.getByRole('region', { name: 'Setup' })
  expect(warning.textContent).toContain(
    'acme/new has no .github/sheepdog.yml on its default branch main. Sheepdog uses your global rules for it, not rules of the repository.',
  )
  expect(within(warning).queryByRole('heading')).toBeNull()
  expect(within(warning).getAllByRole('button').map((button) => button.textContent)).toEqual(['Create sheepdog.yml', 'Edit global rules'])
  expect(screen.getByRole('region', { name: 'safe' })).toBeTruthy()
  expect(sent('GET', '/api/repos/acme/new/classifications')).toHaveLength(1)

  await open(2)
  expect(screen.getByRole('heading', { name: 'PR 2' })).toBeTruthy()
  await emit(sources, global({ defaultBranch: 'main', pullRequest: { number: 7, url: 'https://github.com/acme/new/pull/7' } }))
  expect(screen.getByRole('heading', { name: 'PR 2' })).toBeTruthy()
  expect(screen.getByRole('region', { name: 'Setup' }).textContent).toContain('Sheepdog waits for the merge of pull request #7')

  fireEvent.click(within(screen.getByRole('region', { name: 'Setup' })).getByRole('button', { name: 'Edit global rules' }))
  expect(screen.getByRole('dialog', { name: 'Settings' })).toBeTruthy()
})

it('edits the global rules in the settings, starts from the template, and shows a refused save', async () => {
  let stored = ''
  const { sent } = await signedIn((seen) => {
    if (seen.path === '/api/global-rules/template') return reply(200, { content: template })
    if (seen.path !== '/api/settings') return
    if (seen.method === 'PUT') {
      const text = (seen.body as { globalRules: string }).globalRules
      if (text.includes('version: 2')) return failure(400, 'The global rules are not valid: version must be 1, not 2')
      stored = text
    }
    return reply(200, { repo: 'acme/app', model: 'a/model', hasOpenRouterKey: true, starred: [], autoClassifyRepos: [], autoReviewRepos: [], globalRules: stored })
  })
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Settings' })))
  const dialog = screen.getByRole('dialog', { name: 'Settings' })
  const section = within(dialog).getByRole('region', { name: 'Global rules' })
  const field = within(section).getByLabelText<HTMLTextAreaElement>('Global rules')
  const save = within(section).getByRole('button', { name: 'Save' })
  expect(field.value).toBe('')
  expect(save.hasAttribute('disabled')).toBe(true)

  await act(async () => {
    fireEvent.click(within(section).getByRole('button', { name: 'Start from the template' }))
    await settle()
  })
  expect(field.value).toBe(template)
  expect(within(section).queryByRole('button', { name: 'Start from the template' })).toBeNull()
  expect(sent('PUT', '/api/settings')).toHaveLength(0)

  fireEvent.change(field, { target: { value: 'version: 2\n' } })
  await act(async () => {
    fireEvent.click(save)
    await settle()
  })
  expect(within(dialog).getByText('The global rules are not valid: version must be 1, not 2')).toBeTruthy()
  expect(field.value).toBe('version: 2\n')

  fireEvent.change(field, { target: { value: template } })
  await act(async () => {
    fireEvent.click(save)
    await settle()
  })
  expect(within(section).getByText('Saved')).toBeTruthy()
  expect(field.value).toBe(template)
  expect(save.hasAttribute('disabled')).toBe(true)
  expect(within(dialog).queryByText(/not valid/)).toBeNull()
  expect(sent('PUT', '/api/settings').map((seen) => seen.body)).toEqual([{ globalRules: 'version: 2\n' }, { globalRules: template }])
})

it('changes the sidebar width with the pointer and the keys, keeps it, and resets it with a double click', async () => {
  await signedIn()
  const app = document.querySelector<HTMLElement>('.app')!
  const handle = screen.getByRole('separator', { name: 'Resize the sidebar' })
  const width = () => [app.style.getPropertyValue('--sidebar-width'), handle.getAttribute('aria-valuenow')]
  expect(width()).toEqual(['440px', '440'])

  fireEvent.pointerDown(handle, { clientX: 440, pointerId: 1 })
  fireEvent.pointerMove(handle, { clientX: 500, pointerId: 1 })
  expect(width()).toEqual(['500px', '500'])
  expect(app.className).toBe('app resizing')
  expect(localStorage.getItem('sheepdog.sidebarWidth')).toBeNull()
  fireEvent.pointerUp(handle, { clientX: 540, pointerId: 1 })
  expect(width()).toEqual(['540px', '540'])
  expect(app.className).toBe('app')
  expect(localStorage.getItem('sheepdog.sidebarWidth')).toBe('540')
  // A move without a press changes nothing.
  fireEvent.pointerMove(handle, { clientX: 900, pointerId: 1 })
  expect(width()).toEqual(['540px', '540'])

  fireEvent.keyDown(handle, { key: 'ArrowLeft' })
  fireEvent.keyDown(handle, { key: 'ArrowLeft', shiftKey: true })
  expect(width()).toEqual(['460px', '460'])
  fireEvent.pointerDown(handle, { clientX: 460, pointerId: 2 })
  fireEvent.pointerUp(handle, { clientX: 0, pointerId: 2 })
  expect(width()).toEqual(['300px', '300'])
  fireEvent.pointerDown(handle, { clientX: 300, pointerId: 3 })
  fireEvent.pointerUp(handle, { clientX: 2000, pointerId: 3 })
  expect(width()).toEqual([`${window.innerWidth - 360}px`, String(window.innerWidth - 360)])
  fireEvent.doubleClick(handle)
  expect(width()).toEqual(['440px', '440'])
  expect(localStorage.getItem('sheepdog.sidebarWidth')).toBe('440')
})

it('starts with the kept sidebar width', async () => {
  localStorage.setItem('sheepdog.sidebarWidth', '520')
  await signedIn()
  expect(screen.getByRole('separator', { name: 'Resize the sidebar' }).getAttribute('aria-valuenow')).toBe('520')
})

it('marks the repositories with auto-classify in the picker, and shows them after the starred ones', async () => {
  let autoClassifyRepos = ['acme/web', 'acme/tools']
  const settings = () => ({ repo: 'acme/app', model: 'a/model', hasOpenRouterKey: true, starred: ['acme/tools'], autoClassifyRepos, autoReviewRepos: [] })
  await signedIn((seen) => {
    if (seen.path === '/api/repos') return reply(200, ['acme/app', 'acme/docs', 'acme/web', 'Acme/Tools'])
    if (seen.path === '/api/settings') return reply(200, settings())
    if (seen.path === repoSettingsPath && seen.method === 'GET') return reply(200, { repo: 'acme/app', autoRefresh: true, autoClassify: false })
    if (seen.path === repoSettingsPath) {
      autoClassifyRepos = ['acme/app', ...autoClassifyRepos]
      return reply(200, { repo: 'acme/app', autoRefresh: true, autoClassify: true })
    }
  })
  async function list() {
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /acme\/app/ }))
      await settle()
    })
    const picker = screen.getByRole('dialog', { name: 'Repositories' })
    const items = within(picker)
      .getAllByRole('listitem')
      .map((item) => {
        const flag = within(item).queryByRole('img', { name: 'Auto-classify is on' })
        return `${within(item).getAllByRole('button')[0].textContent}${flag ? ' auto' : ''}`
      })
    fireEvent.click(within(picker).getByRole('button', { name: 'acme/app' }))
    return items
  }
  expect(await list()).toEqual(['Acme/Tools auto', 'acme/web auto', 'acme/app', 'acme/docs'])

  const dialog = await openRepoSettings()
  await act(async () => {
    fireEvent.click(within(dialog).getByRole('switch', { name: 'Auto-classify' }))
    await settle()
  })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
  expect(await list()).toEqual(['Acme/Tools auto', 'acme/app auto', 'acme/web auto', 'acme/docs'])
})

it('shows the diff of the PR below the description, with large files collapsed', async () => {
  await signedIn()
  await open(2)
  const changes = screen.getByRole('region', { name: 'Changes' })
  expect(within(changes).getByRole('heading').textContent).toBe('Changes2 files +402 −1')
  const json = within(changes).getByRole('region', { name: 'package.json' })
  const header = within(json).getByRole('button', { name: /package\.json/ })
  expect(header.getAttribute('aria-expanded')).toBe('true')
  expect(within(json).getAllByRole('row').map((row) => [...row.querySelectorAll('td')].map((cell) => cell.textContent))).toEqual([
    ['@@ -3,3 +3,3 @@'],
    ['3', '3', '   "name": "app",'],
    ['4', '', '-  "expo": "57.0.1"'],
    ['', '4', '+  "expo": "57.2.0"'],
    ['5', '5', ' }'],
  ])
  expect(within(json).getAllByRole('row')[2].className).toBe('delete')
  expect(within(json).getAllByRole('row')[3].className).toBe('add')
  fireEvent.click(header)
  expect(within(json).queryByRole('table')).toBeNull()

  const lock = within(changes).getByRole('region', { name: 'package-lock.json' })
  const lockHeader = within(lock).getByRole('button', { name: /package-lock\.json/ })
  expect(lockHeader.getAttribute('aria-expanded')).toBe('false')
  expect(lockHeader.textContent).toContain('+401 −0')
  expect(within(lock).queryByRole('table')).toBeNull()
  fireEvent.click(lockHeader)
  expect(within(lock).getAllByRole('row')).toHaveLength(402)
  expect(within(changes).queryByText(/larger than 2 MB/)).toBeNull()
})

it('says when the server cut the diff, and shows a failed diff load', async () => {
  let fails = false
  await signedIn((seen) => {
    if (seen.path === '/api/repos/acme/app/pulls/2/diff') {
      return fails ? failure(502, 'Sorry, the diff exceeded the maximum number of files') : reply(200, { diff: prDiff, truncated: true })
    }
    if (seen.path === '/api/repos/acme/app/pulls/3') return reply(200, { ...pullRequest, title: 'PR 3' })
  })
  await open(2)
  const changes = screen.getByRole('region', { name: 'Changes' })
  expect(changes.textContent).toContain('The diff is larger than 2 MB, so Sheepdog shows only its first part.')
  expect(within(changes).getByRole('link', { name: 'See all changes on GitHub' }).getAttribute('href')).toBe(`${rows[0].url}/files`)

  fails = true
  await open(3)
  await open(2)
  expect(within(screen.getByRole('region', { name: 'Changes' })).getByText('Sorry, the diff exceeded the maximum number of files')).toBeTruthy()
  expect(within(screen.getByRole('region', { name: 'Changes' })).queryByRole('region')).toBeNull()
})

it('shows each review finding under its line in the diff, while the review is of the current commit', async () => {
  const finding = (file: string, line: number | null, side: 'new' | 'old', title: string) =>
    ({ severity: 'medium', file, line, side, title, detail: `Detail of ${title}` }) as const
  let headSha = 'sha2'
  await signedIn((seen) => {
    if (seen.path !== '/api/repos/acme/app/pulls/2/review') return
    return reply(200, {
      repo: 'acme/app',
      number: 2,
      headSha,
      model: 'm',
      risk: 'yellow',
      summary: 'Bumps expo.',
      findings: [
        finding('package.json', 4, 'new', 'New version'),
        finding('./package.json', 4, 'old', 'Old version'),
        finding('package.json', 99, 'new', 'Lost line'),
        finding('package-lock.json', null, 'new', 'Whole lockfile'),
        finding('README.md', 1, 'new', 'Not in the diff'),
      ],
      diffTruncated: false,
      prUpdatedAt: '2027-01-15T08:00:00Z',
      createdAt: '2027-01-15T09:00:00Z',
    })
  })
  await open(2)
  const changes = screen.getByRole('region', { name: 'Changes' })
  const json = within(changes).getByRole('region', { name: 'package.json' })
  expect(within(json).getByRole('button', { name: /package\.json/ }).textContent).toContain('3 findings')
  const rows = within(json)
    .getAllByRole('row')
    .map((row) => (row.className === 'comment' ? `> ${row.querySelector('strong')?.textContent}` : row.textContent))
  expect(rows).toEqual([
    '@@ -3,3 +3,3 @@',
    '33   "name": "app",',
    '4-  "expo": "57.0.1"',
    '> Old version',
    '4+  "expo": "57.2.0"',
    '> New version',
    '55 }',
  ])
  expect(within(json).getByText('Lost line').closest('.diff-findings')?.textContent).toContain('package.json:99')

  // The large lockfile opens for its finding, which is about the whole file.
  const lock = within(changes).getByRole('region', { name: 'package-lock.json' })
  expect(within(lock).getByRole('button', { name: /package-lock\.json/ }).getAttribute('aria-expanded')).toBe('true')
  expect(within(lock).getByText('Whole lockfile').closest('.diff-findings')).toBeTruthy()
  expect(within(changes).queryByText('Not in the diff')).toBeNull()
  expect(within(screen.getByRole('region', { name: 'Review' })).getByText('README.md:1')).toBeTruthy()

  headSha = 'sha-old'
  await open(3)
  await open(2)
  expect(within(screen.getByRole('region', { name: 'Changes' })).queryByText('New version')).toBeNull()
  expect(within(screen.getByRole('region', { name: 'Review' })).getByText('New version')).toBeTruthy()
})

it('jumps from a finding in the review to its place in the diff, and opens a collapsed file', async () => {
  await signedIn((seen) => {
    if (seen.path !== '/api/repos/acme/app/pulls/2/review') return
    return reply(200, {
      repo: 'acme/app',
      number: 2,
      headSha: 'sha2',
      model: 'm',
      risk: 'yellow',
      summary: 'Bumps expo.',
      findings: [
        { severity: 'low', file: 'package.json', line: 4, side: 'new', title: 'New version', detail: 'd' },
        { severity: 'high', file: 'package-lock.json', line: 200, side: 'new', title: 'Lock line', detail: 'd' },
        { severity: 'low', file: 'README.md', line: 1, side: 'new', title: 'Elsewhere', detail: 'd' },
      ],
      diffTruncated: false,
      prUpdatedAt: '2027-01-15T08:00:00Z',
      createdAt: '2027-01-15T09:00:00Z',
    })
  })
  await open(2)
  const card = screen.getByRole('region', { name: 'Review' })
  expect(within(card).getAllByRole('button', { name: /:/ }).map((button) => button.textContent)).toEqual([
    'package.json:4',
    'package-lock.json:200',
  ])
  expect(within(card).getByText('README.md:1').closest('button')).toBeNull()

  const lock = within(screen.getByRole('region', { name: 'Changes' })).getByRole('region', { name: 'package-lock.json' })
  fireEvent.click(within(lock).getByRole('button', { name: /package-lock\.json/ }))
  expect(within(lock).queryByText('Lock line')).toBeNull()

  await act(async () => {
    fireEvent.click(within(card).getByRole('button', { name: 'package-lock.json:200' }))
    await settle()
  })
  const target = within(lock).getByText('Lock line').closest<HTMLElement>('.finding')!
  expect(within(lock).getByRole('button', { name: /package-lock\.json/ }).getAttribute('aria-expanded')).toBe('true')
  expect(scrolledTo).toEqual([target])
  expect(document.activeElement).toBe(target)
  expect(target.classList.contains('flash')).toBe(true)
  expect(target.closest('tr')?.previousElementSibling?.textContent).toBe('200+line 200')

  await act(async () => {
    fireEvent.click(within(card).getByRole('button', { name: 'package.json:4' }))
    await settle()
  })
  await act(async () => {
    fireEvent.click(within(card).getByRole('button', { name: 'package.json:4' }))
    await settle()
  })
  const json = within(screen.getByRole('region', { name: 'Changes' })).getByRole('region', { name: 'package.json' })
  const newVersion = within(json).getByText('New version').closest('.finding')
  expect(scrolledTo.slice(1)).toEqual([newVersion, newVersion])
})

it('shows the score and a check for a review below the PR number in the list', async () => {
  const stored = (number: number, headSha: string) => ({
    repo: 'acme/app',
    number,
    headSha,
    model: 'm',
    risk: 'green',
    summary: 's',
    findings: [],
    diffTruncated: false,
    prUpdatedAt: '2027-01-15T08:00:00Z',
    createdAt: '2027-01-15T09:00:00Z',
  })
  const { sources } = await signedIn((seen) => {
    if (seen.path === '/api/repos/acme/app/reviews') return reply(200, [stored(2, 'sha2'), stored(4, 'sha-old')])
  })
  await emit(sources, { ...requests(rows, ['safe', 'docs']), fetchedAt: '2027-01-15T08:00:00Z' })
  const item = (number: number) => screen.getByText(`PR ${number}`).closest('button')!
  const lead = (number: number) => item(number).querySelector('.row-lead')!

  expect(lead(2).firstElementChild?.textContent).toBe('#2')
  expect(within(lead(2) as HTMLElement).getByTitle('Danger 1/10 from CI').textContent).toBe('1')
  expect(within(lead(2) as HTMLElement).getByRole('img', { name: 'Reviewed' }).className).toBe('reviewed')
  expect(item(2).querySelector('.row-title')?.textContent).toBe('PR 2')

  expect(lead(3).textContent).toBe('#3')
  expect(lead(3).querySelector('.row-badges')).toBeNull()

  expect(within(lead(4) as HTMLElement).getByRole('img', { name: 'Reviewed an older commit' }).className).toBe('reviewed outdated')
  expect(lead(4).querySelector('.score')).toBeNull()

  await act(async () => {
    sources.last().emit('review', { repo: 'acme/app', number: 3, review: stored(3, 'sha3') })
    await settle()
  })
  expect(within(lead(3) as HTMLElement).getByRole('img', { name: 'Reviewed' })).toBeTruthy()
})

it('marks the repositories with auto-review in the picker, next to auto-classify, after the starred ones', async () => {
  await signedIn((seen) => {
    if (seen.path === '/api/repos') return reply(200, ['acme/app', 'acme/docs', 'acme/web', 'acme/tools', 'acme/both'])
    if (seen.path === '/api/settings') {
      return reply(200, {
        repo: 'acme/app',
        model: 'a/model',
        hasOpenRouterKey: true,
        starred: ['acme/tools'],
        autoClassifyRepos: ['acme/both'],
        autoReviewRepos: ['acme/web', 'acme/both', 'acme/tools'],
      })
    }
  })
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /acme\/app/ }))
    await settle()
  })
  const picker = screen.getByRole('dialog', { name: 'Repositories' })
  const items = within(picker)
    .getAllByRole('listitem')
    .map((item) =>
      [
        within(item).getAllByRole('button')[0].textContent,
        ...within(item)
          .queryAllByRole('img')
          .map((flag) => flag.getAttribute('aria-label')),
      ].join(' / '),
    )
  expect(items).toEqual([
    'acme/tools / Auto-review is on',
    'acme/web / Auto-review is on',
    'acme/both / Auto-classify is on / Auto-review is on',
    'acme/app',
    'acme/docs',
  ])
})

it('says in the repository settings when auto-review misses the key or the model', async () => {
  await signedIn((seen) => {
    if (seen.path === '/api/settings') {
      return reply(200, { repo: 'acme/app', model: '', hasOpenRouterKey: false, starred: [], autoClassifyRepos: [], autoReviewRepos: [] })
    }
    if (seen.path === repoSettingsPath) return reply(200, { repo: 'acme/app', autoRefresh: true, autoClassify: false, autoReview: true })
  })
  const dialog = await openRepoSettings()
  expect(within(dialog).getByRole<HTMLInputElement>('switch', { name: 'Auto-review' }).checked).toBe(true)
  expect(within(dialog).getByText('Auto-review needs an OpenRouter API key and a default model in Settings.')).toBeTruthy()
  expect(within(dialog).queryByText(/Auto-classify needs/)).toBeNull()
})

it('posts the review to GitHub after a confirmation, once, and then links it', async () => {
  const gate = new Gate()
  let fails = false
  let headSha = 'sha2'
  const stored = {
    repo: 'acme/app',
    number: 2,
    headSha: 'sha2',
    model: 'm',
    risk: 'yellow',
    summary: 's',
    findings: [],
    diffTruncated: false,
    prUpdatedAt: '2027-01-15T08:00:00Z',
    createdAt: '2027-01-15T09:00:00Z',
  }
  const url = 'https://github.com/acme/app/pull/2#pullrequestreview-77'
  const { sources, sent } = await signedIn((seen) => {
    if (seen.path === '/api/repos/acme/app/pulls/2/review') return reply(200, { ...stored, headSha })
    if (seen.path === '/api/repos/acme/app/pulls/2/review/post') {
      return gate.wait().then(() =>
        fails ? failure(409, 'Posting a review needs review.comment: true in .github/sheepdog.yml on main') : reply(200, { ...stored, postedUrl: url }),
      )
    }
  })
  const withPost = (postReviews: boolean) => ({
    ...requests(
      rows.map((row) => (row.number === 2 ? { ...row, postReviews } : row)),
      ['safe', 'docs'],
    ),
    fetchedAt: '2027-01-15T08:00:00Z',
  })
  await emit(sources, withPost(false))
  await open(2)
  const card = () => screen.getByRole('region', { name: 'Review' })
  expect(within(card()).queryByRole('button', { name: 'Post to GitHub' })).toBeNull()

  await emit(sources, withPost(true))
  fireEvent.click(within(card()).getByRole('button', { name: 'Post to GitHub' }))
  let dialog = screen.getByRole('dialog', { name: 'Post the review to GitHub' })
  expect(dialog.textContent).toContain('Everyone who can see the pull request can read it.')
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  expect(sent('POST', '/api/repos/acme/app/pulls/2/review/post')).toHaveLength(0)

  fails = true
  gate.open()
  fireEvent.click(within(card()).getByRole('button', { name: 'Post to GitHub' }))
  await act(async () => {
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Post the review to GitHub' })).getByRole('button', { name: 'Post to GitHub' }))
    await settle()
  })
  expect(within(card()).getByText('Posting a review needs review.comment: true in .github/sheepdog.yml on main')).toBeTruthy()
  expect(within(card()).getByRole('button', { name: 'Post to GitHub' })).toBeTruthy()

  fails = false
  fireEvent.click(within(card()).getByRole('button', { name: 'Post to GitHub' }))
  dialog = screen.getByRole('dialog', { name: 'Post the review to GitHub' })
  await act(async () => {
    fireEvent.click(within(dialog).getByRole('button', { name: 'Post to GitHub' }))
    await settle()
  })
  const link = within(card()).getByRole('link', { name: 'On GitHub' })
  expect(link.getAttribute('href')).toBe(url)
  expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  expect(within(card()).queryByRole('button', { name: 'Post to GitHub' })).toBeNull()
  expect(within(card()).queryByText(/review.comment/)).toBeNull()
  expect(sent('POST', '/api/repos/acme/app/pulls/2/review/post')).toHaveLength(2)

  // A review of an older commit has no button, because its lines can differ from the diff.
  headSha = 'sha-old'
  await open(3)
  await open(2)
  expect(within(card()).queryByRole('button', { name: 'Post to GitHub' })).toBeNull()
})
