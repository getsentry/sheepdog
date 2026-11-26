import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { App } from './App'
import { StoreProvider } from './context'
import { createStore } from './store'
import { failure, fakeEventSources, fakeFetch, reply } from './test/fakes'
import { laneRow, requests } from './test/rows'

afterEach(cleanup)

it('signs in, clears the token field, and lists the PRs of the event stream', async () => {
  let signedIn = false
  const server = fakeFetch(({ method, path }) => {
    if (method === 'POST' && path === '/api/session') {
      signedIn = true
      return reply(200, { login: 'alwx', sessionToken: 'session-token' })
    }
    if (!signedIn) return failure(401, 'Sign in first')
    return path === '/api/settings'
      ? reply(200, { repo: 'acme/app', model: '', hasOpenRouterKey: false })
      : reply(200, [])
  })
  const sources = fakeEventSources()
  const store = createStore({ fetch: server.fetch, EventSource: sources.EventSource })
  await act(() => store.actions.start())
  render(
    <StoreProvider store={store}>
      <App />
    </StoreProvider>,
  )

  const field = screen.getByLabelText<HTMLInputElement>('GitHub token')
  fireEvent.change(field, { target: { value: 'ghp_test' } })
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Sign in' })))
  expect(screen.getByText(/Signed in as alwx/)).toBeTruthy()

  act(() => {
    sources.last().open()
    sources.last().emit('requests', requests([laneRow(2, null), laneRow(3, null)]))
  })
  expect(screen.getAllByRole('listitem').map((item) => item.textContent)).toEqual(['#2', '#3'])
  expect(localStorage.length + sessionStorage.length).toBe(0)
  expect(document.cookie).toBe('')

  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Sign out' })))
  expect(screen.getByLabelText<HTMLInputElement>('GitHub token').value).toBe('')
  expect(sources.last().closed).toBe(true)
})
