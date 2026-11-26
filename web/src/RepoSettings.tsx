import { useEffect } from 'react'
import { useAppState, useStore } from './context'
import { Modal, Spinner, Switch } from './ui'

// The switches of the user for one repository. Mounted only while open, so each open loads them again.
export function RepoSettings({ repo, onClose }: { repo: string; onClose: () => void }) {
  const state = useAppState()
  const { actions } = useStore()
  const current = state.repoSettings?.repo === repo.toLowerCase() ? state.repoSettings : null
  const settings = state.settings
  const missingModel = settings !== null && (!settings.hasOpenRouterKey || !settings.model)

  useEffect(() => {
    void actions.loadRepoSettings(repo)
  }, [actions, repo])

  return (
    <Modal label="Repository settings" className="settings repo-settings" onClose={onClose}>
      <div className="dialog-title">
        <h2>Repository settings</h2>
        <button type="button" className="borderless" aria-label="Close" onClick={onClose}>
          ✕
        </button>
      </div>
      <p className="muted">These settings apply only to {repo}.</p>
      {current === null ? (
        state.repoSettingsError === null && (
          <div className="center">
            <Spinner />
          </div>
        )
      ) : (
        <>
          <section aria-label="Refresh">
            <h3>Refresh</h3>
            <Switch
              label="Auto-refresh"
              checked={current.autoRefresh}
              onChange={(on) => void actions.setRepoPreference('autoRefresh', on)}
            />
            <p className="muted">
              Sheepdog checks GitHub for review requests every 30 seconds. Turn it off to check only with the refresh
              button.
            </p>
          </section>
          <section aria-label="Classify">
            <h3>Classify</h3>
            <Switch
              label="Auto-classify"
              checked={current.autoClassify}
              onChange={(on) => void actions.setRepoPreference('autoClassify', on)}
            />
            <p className="muted">
              Sheepdog gives a pull request a danger score when the score is the only missing condition of an
              approve-and-automerge lane. Each classification sends the diff to OpenRouter with your API key. It uses
              tokens, and you pay for them.
            </p>
            {current.autoClassify && missingModel && (
              <p className="error">Auto-classify needs an OpenRouter API key and a model in Settings.</p>
            )}
          </section>
          <section aria-label="Review">
            <h3>Review</h3>
            <Switch label="Auto-review" checked={false} disabled />
            <p className="muted">
              Sheepdog will review new pull requests by itself. Each review sends the diff to OpenRouter with your API
              key. It uses tokens, and you pay for them. Auto-review does not work yet.
            </p>
          </section>
        </>
      )}
      {state.repoSettingsError && (
        <p className="error" role="alert">
          {state.repoSettingsError}
        </p>
      )}
    </Modal>
  )
}
