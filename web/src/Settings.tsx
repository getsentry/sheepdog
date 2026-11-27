import { useEffect, useState, type FormEvent } from 'react'
import { useAppState, useStore } from './context'
import { notificationsOn, notificationsSupported, setNotifications } from './notifications'
import { Modal, SearchList, Switch } from './ui'

// Mounted only while open, so the key field never keeps a key.
export function Settings({ onClose }: { onClose: () => void }) {
  const state = useAppState()
  const { actions } = useStore()
  const [apiKey, setApiKey] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [signingOutEverywhere, setSigningOutEverywhere] = useState(false)
  const settings = state.settings

  useEffect(() => {
    void actions.loadSettings()
  }, [actions])

  async function saveKey(event: FormEvent) {
    event.preventDefault()
    if (apiKey === '' || saving) return
    setSaving(true)
    if (await actions.saveOpenRouterKey(apiKey)) {
      setApiKey('')
      setSaved(true)
    }
    setSaving(false)
  }

  return (
    <Modal label="Settings" className="settings" onClose={onClose}>
      <div className="dialog-title">
        <h2>Settings</h2>
        <button type="button" className="borderless" aria-label="Close" onClick={onClose}>
          ✕
        </button>
      </div>
      <form className="setting" onSubmit={saveKey}>
        <label htmlFor="openrouter-key">OpenRouter API key</label>
        <div className="setting-value">
          <input
            id="openrouter-key"
            type="password"
            autoComplete="off"
            placeholder={settings?.hasOpenRouterKey ? 'Saved on the server' : 'sk-or-v1-…'}
            value={apiKey}
            onChange={(event) => {
              setApiKey(event.target.value)
              if (event.target.value !== '') setSaved(false)
            }}
          />
          {saved && <span className="muted">Saved</span>}
          <button type="submit" disabled={apiKey === '' || saving}>
            Save
          </button>
        </div>
      </form>
      <div className="setting">
        <span>Default model</span>
        <div className="setting-value">
          <button
            type="button"
            aria-describedby="default-model-help"
            disabled={settings?.hasOpenRouterKey !== true}
            onClick={() => setPickerOpen(true)}
          >
            {settings ? settings.model || 'Select a model' : ''} <span aria-hidden="true">▾</span>
          </button>
        </div>
        <p id="default-model-help" className="muted setting-help">
          Sheepdog uses this model for classifications and reviews when <code>.github/sheepdog.yml</code> of the
          repository, or your global rules, set no <code>classification.model</code> or <code>review.model</code>.
          Classifications and reviews also need this model and the API key.
        </p>
      </div>
      <NotificationsSetting />
      <GlobalRules />
      {state.settingsError && <p className="error">{state.settingsError}</p>}
      <div className="buttons">
        <span className="spacer" />
        <button
          type="button"
          disabled={signingOutEverywhere}
          onClick={async () => {
            setSigningOutEverywhere(true)
            await actions.signOutEverywhere()
            setSigningOutEverywhere(false)
          }}
        >
          Sign out on all devices
        </button>
        <button type="button" onClick={() => void actions.signOut()}>
          Sign out
        </button>
      </div>
      {pickerOpen && (
        <Modal label="Models" className="picker models" onClose={() => setPickerOpen(false)}>
          <ModelList
            onSelect={(model) => {
              setPickerOpen(false)
              void actions.selectModel(model)
            }}
          />
        </Modal>
      )}
    </Modal>
  )
}

function NotificationsSetting() {
  const supported = notificationsSupported()
  const [on, setOn] = useState(notificationsOn)
  const [blocked, setBlocked] = useState(false)

  async function change(next: boolean) {
    setBlocked(false)
    const done = await setNotifications(next)
    setOn(done && next)
    setBlocked(!done)
  }

  return (
    <div className="setting">
      <span>Notifications</span>
      <div className="setting-value">
        <Switch
          label="Desktop notifications"
          checked={on}
          disabled={!supported}
          onChange={(next) => void change(next)}
        />
      </div>
      <p className="muted setting-help">
        {supported
          ? 'Sheepdog shows a notification for a new review request, and when a review or a classification starts by itself or on another device. This browser keeps the choice.'
          : 'This browser has no desktop notifications.'}
      </p>
      {blocked && (
        <p className="error setting-help">
          The browser blocks notifications for this site. Allow them in the site settings of the browser.
        </p>
      )}
    </div>
  )
}

// The field keeps the stored rules until the user edits them.
function GlobalRules() {
  const state = useAppState()
  const { actions } = useStore()
  const [draft, setDraft] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [templateError, setTemplateError] = useState<string | null>(null)
  const text = draft ?? state.settings?.globalRules ?? ''

  function edit(value: string) {
    setDraft(value)
    setSaved(false)
  }

  async function save() {
    if (draft === null || saving) return
    setSaving(true)
    if (await actions.saveGlobalRules(draft)) {
      setDraft(null)
      setSaved(true)
    }
    setSaving(false)
  }

  async function startFromTemplate() {
    setTemplateError(null)
    try {
      const template = await actions.globalRulesTemplate()
      if (template !== null) edit(template)
    } catch (failure) {
      setTemplateError(failure instanceof Error ? failure.message : String(failure))
    }
  }

  return (
    <section className="global-rules" aria-label="Global rules">
      <h3>Global rules</h3>
      <p className="muted">
        Sheepdog uses these rules for your repositories without <code>.github/sheepdog.yml</code> on the default branch.
        They have the same form as that file. Leave the field empty to turn them off.
      </p>
      <textarea
        aria-label="Global rules"
        spellCheck={false}
        placeholder="No global rules"
        value={text}
        disabled={state.settings === null || saving}
        onChange={(event) => edit(event.target.value)}
      />
      {templateError && <p className="error">{templateError}</p>}
      <div className="buttons">
        {text === '' && (
          <button type="button" onClick={() => void startFromTemplate()}>
            Start from the template
          </button>
        )}
        {saved && <span className="muted">Saved</span>}
        <span className="spacer" />
        <button type="button" disabled={draft === null || saving} onClick={() => void save()}>
          Save
        </button>
      </div>
    </section>
  )
}

function ModelList({ onSelect }: { onSelect: (model: string) => void }) {
  const state = useAppState()
  const { actions } = useStore()
  useEffect(() => {
    void actions.loadModels()
  }, [actions])
  return <SearchList prompt="Search models" items={state.models} error={state.modelsError} onSelect={onSelect} />
}
