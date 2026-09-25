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
import { Alert, Badge, Button, Card, EmptyState, Modal, PageHeader, Spinner } from '../components'
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

  const [editingAccount, setEditingAccount] = useState<Account | null>(null)
  const [editLabel, setEditLabel] = useState('')
  const [editIdentity, setEditIdentity] = useState('')

  const [confirmDelete, setConfirmDelete] = useState<Account | null>(null)

  async function reload() {
    try {
      const [ps, as] = await Promise.all([fetchProviders(), fetchAccounts()])
      setProviders(ps)
      setAccounts(as)
      if (!provider && ps.length > 0) {
        const firstImplemented = ps.find((p) => p.implemented)
        setProvider(firstImplemented ? firstImplemented.id : ps[0].id)
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load accounts.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void reload()
  }, [])

  async function onCreate() {
    setFormError(null)
    if (!provider || !label.trim()) {
      setFormError('Provider and label are required.')
      return
    }
    const selected = providers.find((p) => p.id === provider)
    if (!selected?.implemented) {
      setFormError('This provider is not available yet.')
      return
    }
    try {
      const created = await createAccount({ provider, label, identity: identity || undefined })
      setAccounts((prev) => [...prev, created])
      setShowForm(false)
      setLabel('')
      setIdentity('')
    } catch (e) {
      setFormError(e instanceof Error ? e.message : 'Create failed.')
    }
  }

  async function onDelete() {
    if (!confirmDelete) return
    try {
      await deleteAccount(confirmDelete.id)
      setAccounts((prev) => prev.filter((a) => a.id !== confirmDelete.id))
      setConfirmDelete(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Delete failed.')
    }
  }

  async function onToggle(account: Account) {
    try {
      const updated = await updateAccount(account.id, { enabled: !account.enabled })
      setAccounts((prev) => prev.map((a) => (a.id === account.id ? updated : a)))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Update failed.')
    }
  }

  function startEdit(account: Account) {
    setEditingAccount(account)
    setEditLabel(account.label)
    setEditIdentity(account.identity)
  }

  async function onSaveEdit() {
    if (!editingAccount) return
    try {
      const updated = await updateAccount(editingAccount.id, {
        label: editLabel,
        identity: editIdentity,
      })
      setAccounts((prev) => prev.map((a) => (a.id === editingAccount.id ? updated : a)))
      setEditingAccount(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Update failed.')
    }
  }

  const providerName = (id: string) => providers.find((p) => p.id === id)?.display_name ?? id

  if (loading) {
    return (
      <div className="loading-block">
        <Spinner label="Loading accounts" />
      </div>
    )
  }

  return (
    <>
      <PageHeader
        eyebrow="Workspace"
        title="Accounts"
        description="Manage provider accounts and connections."
        action={accounts.length > 0 ? <Button onClick={() => setShowForm(true)}>Add account</Button> : undefined}
      />

      {error && <Alert tone="danger">{error}</Alert>}

      {accounts.length === 0 ? (
        <EmptyState
          title="No accounts yet."
          description="Manage provider accounts and connections. Codex authentication is available; Antigravity support is planned."
          action={<Button onClick={() => setShowForm(true)}>Add account</Button>}
        />
      ) : (
        <ul className="account-list" role="list">
          {accounts.map((account) => (
            <li key={account.id} className="account-card-wrap">
              {account.provider === 'codex' ? (
                <CodexAccount account={account} onChanged={() => void reload()} />
              ) : (
                <Card className="account-card">
                  <div className="account-head">
                    <div>
                      <div className="account-provider">
                        {providerName(account.provider)}{' '}
                        <span className="account-provider-tag">
                          ({providerName(account.provider)})
                        </span>
                      </div>
                      <div className="account-name">{account.label}</div>
                      {account.identity && (
                        <div className="account-identity">{account.identity}</div>
                      )}
                    </div>
                    <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                      <Badge tone={account.enabled ? 'success' : 'neutral'}>
                        {account.enabled ? 'Enabled' : 'Disabled'}
                      </Badge>
                      <Badge tone="neutral">Coming soon</Badge>
                    </div>
                  </div>
                  <p className="metric-detail">This provider is not implemented yet.</p>
                  <div className="account-actions">
                    <Button variant="secondary" onClick={() => startEdit(account)}>
                      Edit
                    </Button>
                    <Button variant="secondary" onClick={() => void onToggle(account)}>
                      {account.enabled ? 'Disable' : 'Enable'}
                    </Button>
                    <Button variant="danger" onClick={() => setConfirmDelete(account)}>
                      Delete
                    </Button>
                  </div>
                </Card>
              )}
            </li>
          ))}
        </ul>
      )}

      {showForm && (
        <Modal title="Add account" onClose={() => setShowForm(false)}>
          <div className="form-grid">
            <div className="field">
              <label htmlFor="account-provider">Provider</label>
              <select
                id="account-provider"
                aria-label="Provider"
                value={provider}
                onChange={(e) => setProvider(e.target.value)}
              >
                {providers.map((p) => (
                  <option key={p.id} value={p.id} disabled={!p.implemented}>
                    {p.display_name}
                    {p.implemented ? '' : ' - Coming soon'}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor="account-label">Label</label>
              <input
                id="account-label"
                aria-label="Label"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                placeholder="Personal"
              />
            </div>
            <div className="field">
              <label htmlFor="account-identity">Identity (optional)</label>
              <input
                id="account-identity"
                aria-label="Identity (optional)"
                value={identity}
                onChange={(e) => setIdentity(e.target.value)}
                placeholder="Shown to you only"
              />
            </div>
          </div>
          {formError && <Alert tone="danger">{formError}</Alert>}
          <div className="modal-actions">
            <Button variant="secondary" onClick={() => setShowForm(false)}>
              Cancel
            </Button>
            <Button onClick={() => void onCreate()}>Save</Button>
          </div>
        </Modal>
      )}

      {editingAccount && (
        <Modal title="Edit account" onClose={() => setEditingAccount(null)}>
          <div className="form-grid">
            <div className="field">
              <label htmlFor="edit-account-label">Label</label>
              <input
                id="edit-account-label"
                aria-label="Edit label"
                value={editLabel}
                onChange={(e) => setEditLabel(e.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor="edit-account-identity">Identity (optional)</label>
              <input
                id="edit-account-identity"
                aria-label="Edit identity"
                value={editIdentity}
                onChange={(e) => setEditIdentity(e.target.value)}
              />
            </div>
          </div>
          <div className="modal-actions">
            <Button variant="secondary" onClick={() => setEditingAccount(null)}>
              Cancel
            </Button>
            <Button onClick={() => void onSaveEdit()}>Save</Button>
          </div>
        </Modal>
      )}

      {confirmDelete && (
        <Modal title="Delete account?" onClose={() => setConfirmDelete(null)}>
          <p>
            This deletes the local profile. Connected provider credentials are handled by the
            provider flow.
          </p>
          <div className="modal-actions">
            <Button variant="secondary" onClick={() => setConfirmDelete(null)}>
              Cancel
            </Button>
            <Button variant="danger" onClick={() => void onDelete()}>
              Confirm delete
            </Button>
          </div>
        </Modal>
      )}
    </>
  )
}
