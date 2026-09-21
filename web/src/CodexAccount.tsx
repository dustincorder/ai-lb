import { useEffect, useState } from 'react'
import {
  cancelCodexLogin,
  fetchCodexLogin,
  fetchCodexStatus,
  logoutCodex,
  refreshCodexQuota,
  startCodexLogin,
  type Account,
  type CodexLogin,
  type CodexQuotaWindow,
  type CodexStatus,
} from './api'

function windowLabel(w: CodexQuotaWindow): string {
  if (w.limit_name) return w.limit_name
  if (w.window_duration_minutes) {
    const mins = w.window_duration_minutes
    if (mins >= 1440 && mins % 1440 === 0) {
      const days = mins / 1440
      return `${days} day window`
    }
    if (mins >= 60 && mins % 60 === 0) return `${mins / 60} hour window`
    return `${mins} min window`
  }
  return w.limit_id || 'Usage window'
}

function formatReset(resetAt?: number): string | null {
  if (resetAt === undefined || resetAt === null) return null
  const d = new Date(resetAt * 1000)
  if (Number.isNaN(d.getTime())) return null
  return d.toLocaleString()
}

function QuotaView({ quota }: { quota: NonNullable<CodexStatus['quota']> }) {
  return (
    <div>
      <h4>Quota{quota.stale ? ' (stale)' : ''}</h4>
      {quota.windows.length === 0 && <p>No quota windows reported.</p>}
      <ul>
        {quota.windows.map((w, i) => (
          <li key={`${w.limit_id || 'window'}-${i}`}>
            <span>{windowLabel(w)}</span>
            {w.used_percent !== undefined && <span> — used {w.used_percent}%</span>}
            {w.remaining_percent !== undefined && <span>, remaining {w.remaining_percent}%</span>}
            {formatReset(w.reset_at) && <span>, resets {formatReset(w.reset_at)}</span>}
          </li>
        ))}
      </ul>
    </div>
  )
}

export function CodexAccount({ account, onChanged }: { account: Account; onChanged: () => void }) {
  const [status, setStatus] = useState<CodexStatus | null>(null)
  const [login, setLogin] = useState<CodexLogin | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)

  useEffect(() => {
    let cancelled = false
    fetchCodexStatus(account.id)
      .then((s) => {
        if (!cancelled) setStatus(s)
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [account.id])

  const waiting = login !== null && (login.state === 'waiting' || login.state === 'idle')

  useEffect(() => {
    if (!waiting || !login) return
    const timer = setInterval(() => {
      fetchCodexLogin(account.id)
        .then((s) => {
          setLogin(s)
          if (s.state === 'succeeded') {
            fetchCodexStatus(account.id)
              .then(setStatus)
              .catch((e: Error) => setError(e.message))
            onChanged()
          }
        })
        .catch((e: Error) => setError(e.message))
    }, 2000)
    return () => clearInterval(timer)
  }, [waiting, login, account.id, onChanged])

  async function onStart(method: 'browser' | 'device') {
    setError(null)
    setBusy(true)
    try {
      setLogin(await startCodexLogin(account.id, method))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Login failed to start.')
    } finally {
      setBusy(false)
    }
  }

  async function onCancel() {
    setError(null)
    try {
      const s = await cancelCodexLogin(account.id, login?.login_id)
      setLogin(s)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Cancel failed.')
    }
  }

  async function onRefresh() {
    setError(null)
    setBusy(true)
    try {
      const quota = await refreshCodexQuota(account.id)
      setStatus((prev) => (prev ? { ...prev, quota } : prev))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Refresh failed.')
    } finally {
      setBusy(false)
    }
  }

  async function onDisconnect() {
    setError(null)
    try {
      await logoutCodex(account.id)
      setStatus((prev) => (prev ? { ...prev, connected: false, quota: undefined } : prev))
      setConfirmDisconnect(false)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Disconnect failed.')
    }
  }

  async function onCopyCode() {
    if (!login?.user_code) return
    try {
      await navigator.clipboard.writeText(login.user_code)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div>
      {error && <p role="alert">{error}</p>}

      {waiting && login && (
        <div>
          {login.method === 'device' ? (
            <div>
              <p>Open:</p>
              <p>{login.verification_url}</p>
              <p>
                Code: <strong>{login.user_code}</strong>
              </p>
              <button type="button" onClick={() => void onCopyCode()}>
                Copy code
              </button>
              {copied && <span> Copied.</span>}
            </div>
          ) : (
            <div>
              <p>Waiting for sign-in…</p>
              {login.auth_url && (
                <a href={login.auth_url} target="_blank" rel="noreferrer">
                  Open sign-in page
                </a>
              )}
            </div>
          )}
          <button type="button" onClick={() => void onCancel()}>
            Cancel
          </button>
        </div>
      )}

      {!waiting && status !== null && !status.connected && (
        <div>
          <p>Not connected</p>
          {login !== null &&
            (login.state === 'failed' || login.state === 'expired') &&
            login.error && <p role="status">{login.error}</p>}
          <button type="button" onClick={() => void onStart('browser')} disabled={busy}>
            Connect with ChatGPT
          </button>{' '}
          <button type="button" onClick={() => void onStart('device')} disabled={busy}>
            Use device code
          </button>
        </div>
      )}

      {!waiting && status !== null && status.connected && (
        <div>
          <p>Connected</p>
          {status.email && <p>{status.email}</p>}
          {status.plan_type && <p>Plan: {status.plan_type}</p>}
          {status.quota && <QuotaView quota={status.quota} />}
          <button type="button" onClick={() => void onRefresh()} disabled={busy}>
            Refresh
          </button>{' '}
          {confirmDisconnect ? (
            <span>
              Disconnect this account?{' '}
              <button type="button" onClick={() => void onDisconnect()}>
                Confirm disconnect
              </button>{' '}
              <button type="button" onClick={() => setConfirmDisconnect(false)}>
                Cancel
              </button>
            </span>
          ) : (
            <button type="button" onClick={() => setConfirmDisconnect(true)}>
              Disconnect
            </button>
          )}
        </div>
      )}
    </div>
  )
}
