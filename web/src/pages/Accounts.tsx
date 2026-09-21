import { useEffect, useState } from 'react'
import {
  createAccount,
  deleteAccount,
  fetchAccounts,
  fetchProviders,
  updateAccount,
  type Account,
  type Provider,
} from '../api'
import { CodexAccount } from '../CodexAccount'

export function Accounts() {
  const [providers, setProviders] = useState<Provider[]>([])
  const [accounts, setAccounts] = useState<Account[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [showForm, setShowForm] = useState(false)
  const [provider, setProvider] = useState('')
  const [label, setLabel] = useState('')
  const [identity, setIdentity] = useState('')
  const [formError, setFormError] = useState<string | null>(null)

  const [editingId, setEditingId] = useState<string | null>(null)
  const [editLabel, setEditLabel] = useState('')
  const [editIdentity, setEditIdentity] = useState('')

  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null)

  const providerName = (id: string) =>
    providers.find((p) => p.id === id)?.display_name ?? id

  async function reload() {
    try {
      const [ps, as] = await Promise.all([fetchProviders(), fetchAccounts()])
      setProviders(ps)
      setAccounts(as)
      if (!provider && ps.length > 0) setProvider(ps[0].id)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load accounts.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void reload()
  }, [])

  if (loading) return <p>Loading…</p>
  if (error) return <p role="alert">Failed to load accounts: {error}</p>

  async function onCreate() {
    setFormError(null)
    if (!provider) {
      setFormError('Choose a provider.')
      return
    }
    try {
      const created = await createAccount({
        provider,
        label,
        identity: identity || undefined,
      })
      setAccounts((prev) => [...prev, created])
      setShowForm(false)
      setLabel('')
      setIdentity('')
    } catch (e) {
      setFormError(e instanceof Error ? e.message : 'Create failed.')
    }
  }

  function startEdit(a: Account) {
    setEditingId(a.id)
    setEditLabel(a.label)
    setEditIdentity(a.identity)
  }

  async function onSaveEdit(a: Account) {
    setFormError(null)
    try {
      const updated = await updateAccount(a.id, { label: editLabel, identity: editIdentity })
      setAccounts((prev) => prev.map((x) => (x.id === a.id ? updated : x)))
      setEditingId(null)
    } catch (e) {
      setFormError(e instanceof Error ? e.message : 'Update failed.')
    }
  }

  async function onToggle(a: Account) {
    try {
      const updated = await updateAccount(a.id, { enabled: !a.enabled })
      setAccounts((prev) => prev.map((x) => (x.id === a.id ? updated : x)))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Update failed.')
    }
  }

  async function onDelete(id: string) {
    try {
      await deleteAccount(id)
      setAccounts((prev) => prev.filter((x) => x.id !== id))
      setConfirmDeleteId(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Delete failed.')
    }
  }

  return (
    <section>
      <h2>Accounts</h2>
      <p>
        Manage provider accounts and connections. Codex authentication is
        available; Antigravity support is planned.
      </p>

      {accounts.length === 0 && !showForm ? (
        <div>
          <p>No accounts yet.</p>
          <button type="button" onClick={() => setShowForm(true)}>
            Add account
          </button>
        </div>
      ) : (
        <div>
          <ul>
            {accounts.map((a) => (
              <li key={a.id}>
                {editingId === a.id ? (
                  <div>
                    <label>
                      Label
                      <input
                        aria-label="Edit label"
                        value={editLabel}
                        onChange={(e) => setEditLabel(e.target.value)}
                      />
                    </label>
                    <label>
                      Identity
                      <input
                        aria-label="Edit identity"
                        value={editIdentity}
                        onChange={(e) => setEditIdentity(e.target.value)}
                      />
                    </label>
                    <button type="button" onClick={() => void onSaveEdit(a)}>
                      Save
                    </button>
                    <button type="button" onClick={() => setEditingId(null)}>
                      Cancel
                    </button>
                  </div>
                ) : (
                  <div>
                    <strong>{a.label}</strong> ({providerName(a.provider)})
                    {a.identity && <span> — {a.identity}</span>}{' '}
                    <span>{a.enabled ? 'Enabled' : 'Disabled'}</span>{' '}
                    <span>{a.connected ? 'Connected' : 'Not connected'}</span>{' '}
                    <button type="button" onClick={() => startEdit(a)}>
                      Edit
                    </button>{' '}
                    <button type="button" onClick={() => void onToggle(a)}>
                      {a.enabled ? 'Disable' : 'Enable'}
                    </button>{' '}
                    {a.provider === 'codex' && (
                      <CodexAccount account={a} onChanged={() => void reload()} />
                    )}
                    {confirmDeleteId === a.id ? (
                      <span>
                        Delete this account?{' '}
                        <button type="button" onClick={() => void onDelete(a.id)}>
                          Confirm delete
                        </button>{' '}
                        <button type="button" onClick={() => setConfirmDeleteId(null)}>
                          Cancel
                        </button>
                      </span>
                    ) : (
                      <button type="button" onClick={() => setConfirmDeleteId(a.id)}>
                        Delete
                      </button>
                    )}
                  </div>
                )}
              </li>
            ))}
          </ul>
          <button type="button" onClick={() => setShowForm((v) => !v)}>
            {showForm ? 'Close' : 'Add account'}
          </button>
        </div>
      )}

      {showForm && (
        <div>
          <h3>Add account</h3>
          <label>
            Provider
            <select
              aria-label="Provider"
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
            >
              {providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.display_name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Label
            <input
              aria-label="Label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
            />
          </label>
          <label>
            Identity (optional)
            <input
              aria-label="Identity"
              value={identity}
              onChange={(e) => setIdentity(e.target.value)}
            />
          </label>
          <button type="button" onClick={() => void onCreate()}>
            Save
          </button>
        </div>
      )}
      {formError && <p role="alert">{formError}</p>}
    </section>
  )
}
