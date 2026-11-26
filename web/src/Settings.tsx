import { useEffect, useState, type FormEvent } from 'react'
import { useAppState, useStore } from './context'
import { Modal, SearchList } from './ui'

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
        <span>Classification model</span>
        <div className="setting-value">
          <button type="button" disabled={settings?.hasOpenRouterKey !== true} onClick={() => setPickerOpen(true)}>
            {settings ? settings.model || 'Select a model' : ''} <span aria-hidden="true">▾</span>
          </button>
        </div>
      </div>
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

function ModelList({ onSelect }: { onSelect: (model: string) => void }) {
  const state = useAppState()
  const { actions } = useStore()
  useEffect(() => {
    void actions.loadModels()
  }, [actions])
  return <SearchList prompt="Search models" items={state.models} error={state.modelsError} onSelect={onSelect} />
}
