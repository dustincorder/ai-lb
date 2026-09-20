import { useState } from 'react'
import { postUpdateCheck, postUpdateDownload, type UpdateState } from './api'

function headline(state: UpdateState): string {
  if (state.available_channel === 'nightly') return 'New nightly build available.'
  if (state.channel === 'nightly') {
    return `Stable release ${state.available_version} is available.`
  }
  return `ai-lb ${state.available_version} is available.`
}

export function UpdateBanner({ initial }: { initial: UpdateState }) {
  const [state, setState] = useState(initial)
  const [dismissed, setDismissed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string | null>(null)

  if (dismissed || state.status !== 'available' || !state.available_version) return null

  async function onDownload() {
    setBusy(true)
    setMessage(null)
    try {
      const res = await postUpdateDownload()
      setMessage(`Downloaded and verified (SHA-256): ${res.path}`)
    } catch (e) {
      setMessage(e instanceof Error ? `Download failed: ${e.message}` : 'Download failed.')
    } finally {
      setBusy(false)
    }
  }

  async function onRecheck() {
    setBusy(true)
    try {
      setState(await postUpdateCheck())
    } catch (e) {
      setMessage(e instanceof Error ? `Check failed: ${e.message}` : 'Check failed.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div role="status" aria-label="update available">
      <p>{headline(state)}</p>
      {state.asset ? (
        <button type="button" onClick={onDownload} disabled={busy}>
          {busy ? 'Working…' : 'Download update'}
        </button>
      ) : (
        <p>{state.asset_note ?? 'No download for this platform in the release.'}</p>
      )}
      {state.release_url && (
        <a href={state.release_url} target="_blank" rel="noreferrer">
          Release notes
        </a>
      )}{' '}
      <button type="button" onClick={onRecheck} disabled={busy}>
        Check again
      </button>{' '}
      <button type="button" onClick={() => setDismissed(true)}>
        Dismiss
      </button>
      {message && <p>{message}</p>}
    </div>
  )
}
