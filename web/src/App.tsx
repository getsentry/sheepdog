import { useState, type FormEvent } from 'react'
import { useAppState, useStore } from './context'

export function App() {
  const state = useAppState()
  const { actions } = useStore()
  const [token, setToken] = useState('')
  const [signingIn, setSigningIn] = useState(false)

  async function signIn(event: FormEvent) {
    event.preventDefault()
    setSigningIn(true)
    if (await actions.signIn(token)) setToken('')
    setSigningIn(false)
  }

  if (state.auth === 'checking') return <p>Loading…</p>
  if (state.auth === 'signedOut') {
    return (
      <form onSubmit={signIn}>
        <h1>Sign in to Sheepdog</h1>
        <input
          type="password"
          aria-label="GitHub token"
          autoComplete="off"
          value={token}
          onChange={(event) => setToken(event.target.value)}
        />
        <button type="submit" disabled={signingIn || token.trim() === ''}>
          Sign in
        </button>
        {state.signInError && <p role="alert">{state.signInError}</p>}
      </form>
    )
  }
  return (
    <main>
      <p>
        Signed in as {state.login}. {state.requests?.repo}
      </p>
      {state.connectionError && <p role="alert">{state.connectionError}</p>}
      <ul aria-label="Pull requests">
        {state.requests?.rows.map((row) => <li key={row.number}>#{row.number}</li>)}
      </ul>
      <button onClick={() => void actions.signOut()}>Sign out</button>
    </main>
  )
}
