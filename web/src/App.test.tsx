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
})

afterEach(cleanup)

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
        return reply(200, { repo: 'acme/app', model: 'a/model', hasOpenRouterKey: true })
      case 'GET /api/repos':
        return reply(200, ['acme/app'])
      case 'GET /api/models':
        return reply(200, ['a/model', 'b/model'])
      case 'GET /api/repos/acme/app/classifications':
        return reply(200, [])
      case 'GET /api/repos/acme/app/pulls/2':
        return reply(200, pullRequest)
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
    fireEvent.click(within(settings).getByRole('button', { name: /a\/model/ }))
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
    'acme/new has no .github/sheepdog.yml on its default branch main. Sheepdog works only with repositories that have this file.',
  )
  expect(within(warning).getByRole('button', { name: 'Create sheepdog.yml' })).toBeTruthy()
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
  expect(within(waiting).queryByRole('button')).toBeNull()
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
