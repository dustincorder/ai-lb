import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Accounts } from './pages/Accounts'

interface Stored {
  id: string
  provider: string
  label: string
  identity: string
  enabled: boolean
  connected: boolean
}

let store: Stored[]
let nextId: number

function mockFetch() {
  return vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    if (url === '/api/providers') {
      return Response.json({
        providers: [
          { id: 'codex', display_name: 'Codex', implemented: true },
          { id: 'antigravity', display_name: 'Antigravity', implemented: false },
        ],
      })
    }
    if (url === '/api/accounts' && method === 'GET') {
      return Response.json({ accounts: store })
    }
    if (url === '/api/accounts' && method === 'POST') {
      const body = JSON.parse(init?.body as string) as {
        provider: string
        label: string
        identity?: string
      }
      const created: Stored = {
        id: `acc-${nextId++}`,
        provider: body.provider,
        label: body.label,
        identity: body.identity ?? '',
        enabled: true,
        connected: false,
      }
      store.push(created)
      return new Response(JSON.stringify(created), { status: 201 })
    }
    const match = url.match(/^\/api\/accounts\/([^/]+)$/)
    if (match) {
      const id = decodeURIComponent(match[1])
      const idx = store.findIndex((a) => a.id === id)
      if (method === 'PATCH' && idx >= 0) {
        const patch = JSON.parse(init?.body as string) as Partial<Stored>
        store[idx] = { ...store[idx], ...patch }
        return Response.json(store[idx])
      }
      if (method === 'DELETE' && idx >= 0) {
        store.splice(idx, 1)
        return new Response(null, { status: 204 })
      }
      return new Response(JSON.stringify({ error: 'account_not_found' }), { status: 404 })
    }
    if (url.includes('/codex/status')) {
      return Response.json({ connected: false, requires_openai_auth: true, observed_at: '' })
    }
    return new Response('{}', { status: 200 })
  })
}

describe('Accounts page', () => {
  beforeEach(() => {
    store = []
    nextId = 1
    vi.stubGlobal('fetch', mockFetch())
  })

  it('shows the empty state with an add button', async () => {
    render(<Accounts />)
    expect(await screen.findByText('No accounts yet.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add account' })).toBeInTheDocument()
    expect(screen.getByText(/Codex authentication is available/i)).toBeInTheDocument()
  })

  it('loads provider options and lists accounts', async () => {
    store = [
      {
        id: 'acc-9',
        provider: 'codex',
        label: 'Personal',
        identity: 'me@example.com',
        enabled: true,
        connected: false,
      },
    ]
    render(<Accounts />)
    expect(await screen.findByText('Personal')).toBeInTheDocument()
    expect(screen.getByText('(Codex)')).toBeInTheDocument()
    expect(screen.getByText('Not connected')).toBeInTheDocument()
  })

  it('creates an account through the form', async () => {
    const user = userEvent.setup()
    render(<Accounts />)
    await user.click(await screen.findByRole('button', { name: 'Add account' }))

    const provider = (await screen.findByLabelText('Provider')) as HTMLSelectElement
    expect(provider.options.length).toBe(2)
    await user.selectOptions(provider, 'codex')
    await user.type(screen.getByLabelText('Label'), 'Work')
    await user.type(screen.getByLabelText(/Identity/), 'work@example.com')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Work')).toBeInTheDocument()
    expect(screen.getByText('(Codex)')).toBeInTheDocument()
    expect(store.length).toBe(1)
  })

  it('enables and disables an account', async () => {
    const user = userEvent.setup()
    store = [
      {
        id: 'acc-9',
        provider: 'codex',
        label: 'Personal',
        identity: '',
        enabled: true,
        connected: false,
      },
    ]
    render(<Accounts />)
    await screen.findByText('Personal')
    await user.click(screen.getByRole('button', { name: 'Disable' }))
    expect(await screen.findByText('Disabled')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Enable' }))
    expect(await screen.findByText('Enabled')).toBeInTheDocument()
  })

  it('deletes an account after inline confirmation', async () => {
    const user = userEvent.setup()
    store = [
      {
        id: 'acc-9',
        provider: 'codex',
        label: 'Personal',
        identity: '',
        enabled: true,
        connected: false,
      },
    ]
    render(<Accounts />)
    await screen.findByText('Personal')
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    const row = screen.getByText('Personal').closest('li')
    if (!row) throw new Error('row missing')
    await user.click(within(row).getByRole('button', { name: 'Confirm delete' }))
    expect(await screen.findByText('No accounts yet.')).toBeInTheDocument()
    expect(store.length).toBe(0)
  })

  it('marks implemented:false provider as unavailable and coming soon', async () => {
    const user = userEvent.setup()
    render(<Accounts />)
    await user.click(await screen.findByRole('button', { name: 'Add account' }))

    const provider = (await screen.findByLabelText('Provider')) as HTMLSelectElement
    const antigravity = within(provider).getByRole('option', { name: /Antigravity/ }) as HTMLOptionElement
    expect(antigravity.disabled).toBe(true)
    expect(antigravity.textContent).toContain('Coming soon')
  })

  it('does not expose internal UUIDs or credentials in the UI', async () => {
    store = [
      {
        id: 'uuid-secret-999',
        provider: 'codex',
        label: 'Secret Profile',
        identity: 'secret@example.com',
        enabled: true,
        connected: false,
      },
    ]
    render(<Accounts />)
    expect(await screen.findByText('Secret Profile')).toBeInTheDocument()
    expect(screen.queryByText('uuid-secret-999')).not.toBeInTheDocument()
    expect(screen.queryByText(/credentials_ref/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/provider_account_id/i)).not.toBeInTheDocument()
  })
})
