import { useState, type FormEvent } from 'react'
import { useAppState, useStore } from './context'
import { ExternalLink, Spinner } from './ui'

const newTokenURL = 'https://github.com/settings/tokens/new?scopes=repo,read:org&description=Sheepdog%20Token'

// The form unmounts after a sign-in, so the token field does not keep the token.
export function SignIn() {
  const state = useAppState()
  const { actions } = useStore()
  const [token, setToken] = useState('')
  const [signingIn, setSigningIn] = useState(false)

  async function signIn(event: FormEvent) {
    event.preventDefault()
    if (signingIn || token.trim() === '') return
    setSigningIn(true)
    await actions.signIn(token)
    setSigningIn(false)
  }

  return (
    <div className="sign-in">
      <form onSubmit={signIn}>
        <h1>Sign in to Sheepdog</h1>
        <label>
          GitHub token
          <input
            type="password"
            placeholder="ghp_…"
            autoComplete="off"
            autoFocus
            value={token}
            onChange={(event) => setToken(event.target.value)}
          />
        </label>
        <p className="muted">
          Use a classic personal access token with the scopes repo and read:org. If your organization uses SAML single
          sign-on, click Configure SSO next to the token on GitHub and authorize it for the organization.
        </p>
        {state.signInError && (
          <p className="error" role="alert">
            {state.signInError}
          </p>
        )}
        <div className="buttons">
          <ExternalLink href={newTokenURL} className="button">
            Create token on GitHub
          </ExternalLink>
          <span className="spacer" />
          {signingIn && <Spinner label="Signing in" />}
          <button type="submit" className="primary" disabled={signingIn || token.trim() === ''}>
            Sign in
          </button>
        </div>
      </form>
    </div>
  )
}
