import { useEffect, useState } from 'react'
import {
  cancelCodexLogin,
  deleteAccount,
  fetchCodexLogin,
  fetchCodexStatus,
  launchCodex,
  logoutCodex,
  refreshCodexQuota,
  startCodexLogin,
  updateAccount,
  type Account,
  type CodexLogin,
  type CodexQuota,
  type CodexStatus,
} from './api'
import { Alert, Badge, Button, Card, Modal, ProgressBar } from './components'

function resetLabel(value?: number) {
  if (!value) return null
  const date = new Date(value * 1000)
  return Number.isNaN(date.getTime())
    ? null
    : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function QuotaView({ quota }: { quota: CodexQuota }) {
  return (
    <div className="quota-list">
      {quota.windows.length === 0 && <p className="metric-detail">No quota windows reported.</p>}
      {quota.windows.map((window, index) => {
        const remaining =
          window.remaining_percent ??
          (window.used_percent !== undefined ? 100 - window.used_percent : 0)
        return (
          <div key={`${window.limit_id || 'window'}-${index}`}>
            <div className="quota-name">
              <span>
                {window.limit_name ||
                  window.limit_id ||
                  `${window.window_duration_minutes || ''} minute window`}
              </span>
              <strong>{remaining}% remaining</strong>
            </div>
            <ProgressBar value={remaining} label={`${remaining}% remaining`} />
            <div className="metric-detail">
              {window.used_percent !== undefined && <span>used {window.used_percent}%</span>}
              {window.remaining_percent !== undefined && (
                <span> · remaining {window.remaining_percent}%</span>
              )}
              {resetLabel(window.reset_at) && <span> · resets {resetLabel(window.reset_at)}</span>}
            </div>
          </div>
        )
      })}
    </div>
  )
}

export function CodexAccount({
  account,
  onChanged,
}: {
  account: Account
  onChanged: () => void
}) {
  const [accountState, setAccountState] = useState(account)
  const [status, setStatus] = useState<CodexStatus | null>(null)
  const [login, setLogin] = useState<CodexLogin | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [workingDir, setWorkingDir] = useState('')
  const [launchSuccess, setLaunchSuccess] = useState(false)
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [editing, setEditing] = useState(false)
  const [editLabel, setEditLabel] = useState(account.label)
  const [editIdentity, setEditIdentity] = useState(account.identity)

  useEffect(() => {
    setAccountState(account)
    setEditLabel(account.label)
    setEditIdentity(account.identity)
  }, [account])

  useEffect(() => {
    let cancelled = false
    fetchCodexStatus(accountState.id)
      .then((value) => {
        if (!cancelled) setStatus(value)
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [accountState.id])

  const waiting = login !== null && (login.state === 'waiting' || login.state === 'idle')

  useEffect(() => {
    if (!waiting || !login) return
    const timer = setInterval(() => {
      fetchCodexLogin(accountState.id)
        .then((next) => {
          setLogin(next)
          if (next.state === 'succeeded') {
            fetchCodexStatus(accountState.id)
              .then(setStatus)
              .catch(() => undefined)
            onChanged()
          }
        })
        .catch((e: Error) => setError(e.message))
    }, 2000)
    return () => clearInterval(timer)
  }, [waiting, login, accountState.id, onChanged])

  async function connect(method: 'browser' | 'device') {
    setError(null)
    setBusy(true)
    try {
      setLogin(await startCodexLogin(accountState.id, method))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Login failed.')
    } finally {
      setBusy(false)
    }
  }

  async function launch() {
    setError(null)
    setLaunchSuccess(false)
    setBusy(true)
    try {
      await launchCodex(accountState.id, workingDir)
      setLaunchSuccess(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unable to launch Codex CLI.')
    } finally {
      setBusy(false)
    }
  }

  async function refresh() {
    setError(null)
    setBusy(true)
    try {
      const quota = await refreshCodexQuota(accountState.id)
      setStatus((previous) => (previous ? { ...previous, quota } : previous))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Refresh failed.')
    } finally {
      setBusy(false)
    }
  }

  async function disconnect() {
    setError(null)
    try {
      await logoutCodex(accountState.id)
      setStatus((previous) =>
        previous ? { ...previous, connected: false, quota: undefined } : previous,
      )
      setConfirmDisconnect(false)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Disconnect failed.')
    }
  }

  async function onToggle() {
    setError(null)
    try {
      const updated = await updateAccount(accountState.id, { enabled: !accountState.enabled })
      setAccountState(updated)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Update failed.')
    }
  }

  async function onSaveEdit() {
    setError(null)
    try {
      const updated = await updateAccount(accountState.id, {
        label: editLabel,
        identity: editIdentity,
      })
      setAccountState(updated)
      setEditing(false)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed.')
    }
  }

  async function onDelete() {
    setError(null)
    try {
      await deleteAccount(accountState.id)
      setConfirmDelete(false)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Delete failed.')
    }
  }

  const isConnected = status !== null ? status.connected : accountState.connected
  const displayIdentity = status?.email || accountState.identity

  return (
    <Card className="account-card">
      <div className="account-head">
        <div>
          <div className="account-provider">
            Codex <span className="account-provider-tag">(Codex)</span>
          </div>
          <div className="account-name">{accountState.label}</div>
          {displayIdentity && <div className="account-identity">{displayIdentity}</div>}
        </div>
        <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
          <Badge tone={accountState.enabled ? 'success' : 'neutral'}>
            {accountState.enabled ? 'Enabled' : 'Disabled'}
          </Badge>
          <Badge tone={isConnected ? 'success' : 'neutral'}>
            {isConnected ? 'Connected' : 'Not connected'}
          </Badge>
        </div>
      </div>

      {error && <Alert tone="danger">{error}</Alert>}

      {!status && !error && (
        <div className="loading-block">
          <span className="spinner-wrap" role="status">
            <span className="spinner" />
            Loading account status...
          </span>
        </div>
      )}

      {!isConnected && !waiting && (
        <div className="account-actions">
          <Button onClick={() => void connect('browser')} disabled={busy}>
            Connect with ChatGPT
          </Button>
          <Button variant="secondary" onClick={() => void connect('device')} disabled={busy}>
            Use device code
          </Button>
          <Button variant="secondary" onClick={() => setEditing(true)}>
            Edit
          </Button>
          <Button variant="secondary" onClick={() => void onToggle()}>
            {accountState.enabled ? 'Disable' : 'Enable'}
          </Button>
          <Button variant="danger" onClick={() => setConfirmDelete(true)}>
            Delete
          </Button>
        </div>
      )}

      {waiting && login && (
        <Card>
          <h3>{login.method === 'device' ? 'Enter this code' : 'Waiting for ChatGPT sign-in'}</h3>
          {login.method !== 'device' && <p>Waiting for sign-in…</p>}
          {login.user_code && (
            <>
              <p className="technical">{login.user_code}</p>
              <Button
                variant="secondary"
                onClick={() => void navigator.clipboard?.writeText(login.user_code || '')}
              >
                Copy code
              </Button>
            </>
          )}
          {login.verification_url && <p className="technical">{login.verification_url}</p>}
          {login.auth_url && (
            <a href={login.auth_url} target="_blank" rel="noreferrer">
              Open sign-in page
            </a>
          )}
          <div className="account-actions section">
            <Button
              variant="secondary"
              onClick={() => {
                void cancelCodexLogin(accountState.id, login.login_id).then(setLogin)
              }}
            >
              Cancel
            </Button>
          </div>
        </Card>
      )}

      {!waiting && login?.error && <Alert tone="danger">{login.error}</Alert>}

      {isConnected && (
        <>
          <div className="status-line">
            <span className="metric-detail">Plan</span>
            <strong>Plan: {status?.plan_type || 'ChatGPT account'}</strong>
          </div>
          {status?.quota && (
            <div>
              <div className="section-title">
                <h3>Quota{status.quota.stale ? ' (stale)' : ''}</h3>
                {status.quota.stale && <Badge tone="warning">Stale</Badge>}
              </div>
              <QuotaView quota={status.quota} />
            </div>
          )}
          <div className="field">
            <label htmlFor={`working-dir-${accountState.id}`}>Working directory</label>
            <input
              id={`working-dir-${accountState.id}`}
              aria-label="Working directory"
              value={workingDir}
              onChange={(e) => setWorkingDir(e.target.value)}
              placeholder="Home directory"
            />
            <p className="helper">Leave empty to start in your home directory.</p>
          </div>
          <div className="account-actions">
            <Button onClick={() => void launch()} disabled={busy}>
              {busy ? 'Launching...' : 'Launch Codex CLI'}
            </Button>
            <Button variant="secondary" onClick={() => void refresh()} disabled={busy}>
              Refresh
            </Button>
            <Button variant="secondary" onClick={() => setEditing(true)}>
              Edit
            </Button>
            <Button variant="secondary" onClick={() => void onToggle()}>
              {accountState.enabled ? 'Disable' : 'Enable'}
            </Button>
            <Button variant="ghost" onClick={() => setConfirmDisconnect(true)}>
              Disconnect
            </Button>
            <Button variant="danger" onClick={() => setConfirmDelete(true)}>
              Delete
            </Button>
          </div>
          {launchSuccess && <Alert tone="success">Codex CLI launched.</Alert>}
        </>
      )}

      {confirmDisconnect && (
        <Modal title="Disconnect account?" onClose={() => setConfirmDisconnect(false)}>
          <p>
            This removes the connection from this local profile. The provider account itself is not
            changed.
          </p>
          <div className="modal-actions">
            <Button variant="secondary" onClick={() => setConfirmDisconnect(false)}>
              Cancel
            </Button>
            <Button variant="danger" onClick={() => void disconnect()}>
              Confirm disconnect
            </Button>
          </div>
        </Modal>
      )}

      {confirmDelete && (
        <Modal title="Delete account?" onClose={() => setConfirmDelete(false)}>
          <p>
            This deletes the local profile. Connected provider credentials are handled by the provider
            flow.
          </p>
          <div className="modal-actions">
            <Button variant="secondary" onClick={() => setConfirmDelete(false)}>
              Cancel
            </Button>
            <Button variant="danger" onClick={() => void onDelete()}>
              Confirm delete
            </Button>
          </div>
        </Modal>
      )}

      {editing && (
        <Modal title="Edit account" onClose={() => setEditing(false)}>
          <div className="form-grid">
            <div className="field">
              <label htmlFor={`edit-label-${accountState.id}`}>Label</label>
              <input
                id={`edit-label-${accountState.id}`}
                aria-label="Edit label"
                value={editLabel}
                onChange={(e) => setEditLabel(e.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor={`edit-identity-${accountState.id}`}>Identity (optional)</label>
              <input
                id={`edit-identity-${accountState.id}`}
                aria-label="Edit identity"
                value={editIdentity}
                onChange={(e) => setEditIdentity(e.target.value)}
              />
            </div>
          </div>
          <div className="modal-actions">
            <Button variant="secondary" onClick={() => setEditing(false)}>
              Cancel
            </Button>
            <Button onClick={() => void onSaveEdit()}>Save</Button>
          </div>
        </Modal>
      )}
    </Card>
  )
}
