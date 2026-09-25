import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CodexAccount } from './CodexAccount'
import type { Account } from './api'

const profile: Account = {
  id: 'acc-1',
  provider: 'codex',
  label: 'Personal',
  identity: '',
  enabled: true,
  connected: false,
  created_at: '',
  updated_at: '',
}

interface Route {
  method: string
  match: (url: string) => boolean
  respond: () => Response
}

let routes: Route[]
let calls: string[]

function mockFetch() {
  return vi.fn(async (url: string, init?: RequestInit) => {

    const method = init?.method ?? 'GET'
    calls.push(`${method} ${url}`)
    for (const r of routes) {
      if (r.method === method && r.match(url)) return r.respond()
    }
    return new Response('{}', { status: 200 })
  })
}

function disconnected() {
  return Response.json({ connected: false, requires_openai_auth: true, observed_at: '' })
}

describe('CodexAccount', () => {
  beforeEach(() => {
    routes = [
      {
        method: 'GET',
        match: (u) => u.endsWith('/codex/status'),
        respond: disconnected,
      },
    ]
    calls = []
    vi.stubGlobal('fetch', mockFetch())
  })

  it('shows Connect actions when disconnected', async () => {
    render(<CodexAccount account={profile} onChanged={() => undefined} />)
    expect(await screen.findByText('Not connected')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Connect with ChatGPT' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Use device code' })).toBeInTheDocument()
  })

  it('shows the browser waiting state with sign-in link and cancel', async () => {
    const user = userEvent.setup()
    routes.push({
      method: 'POST',
      match: (u) => u.endsWith('/codex/login'),
      respond: () =>
        Response.json({
          account_id: 'acc-1',
          login_id: 'login-1',
          method: 'browser',
          state: 'waiting',
          auth_url: 'https://example.com/auth',
        }),
    })
    render(<CodexAccount account={profile} onChanged={() => undefined} />)
    await user.click(await screen.findByRole('button', { name: 'Connect with ChatGPT' }))
    expect(await screen.findByText('Waiting for sign-in…')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open sign-in page' })).toHaveAttribute(
      'href',
      'https://example.com/auth',
    )
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  })

  it('shows the device code state', async () => {
    const user = userEvent.setup()
    routes.push({
      method: 'POST',
      match: (u) => u.endsWith('/codex/login'),
      respond: () =>
        Response.json({
          account_id: 'acc-1',
          login_id: 'login-2',
          method: 'device',
          state: 'waiting',
          verification_url: 'https://example.com/device',
          user_code: 'ABCD-1234',
        }),
    })
    render(<CodexAccount account={profile} onChanged={() => undefined} />)
    await user.click(await screen.findByRole('button', { name: 'Use device code' }))
    expect(await screen.findByText('https://example.com/device')).toBeInTheDocument()
    expect(screen.getByText('ABCD-1234')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Copy code' })).toBeInTheDocument()
  })

  it('shows connected plan/quota with refresh and disconnect', async () => {
    const user = userEvent.setup()
    routes = [
      {
        method: 'GET',
        match: (u) => u.endsWith('/codex/status'),
        respond: () =>
          Response.json({
            connected: true,
            email: 'u@example.com',
            plan_type: 'plus',
            requires_openai_auth: true,
            observed_at: '',
            quota: {
              windows: [
                { limit_id: 'codex', limit_name: 'Codex', used_percent: 30, remaining_percent: 70 },
              ],
              updated_at: '',
              stale: false,
            },
          }),
      },
      {
        method: 'POST',
        match: (u) => u.endsWith('/codex/refresh'),
        respond: () => Response.json({ windows: [], updated_at: '', stale: false }),
      },
      {
        method: 'POST',
        match: (u) => u.endsWith('/codex/logout'),
        respond: () => Response.json({ connected: false }),
      },
      {
        method: 'POST',
        match: (u) => u.endsWith('/codex/launch'),
        respond: () => Response.json({ status: 'launched' }),
      },
    ]
    let changed = 0
    render(<CodexAccount account={profile} onChanged={() => changed++} />)
    expect(await screen.findByText('Connected')).toBeInTheDocument()
    expect(screen.getByText('u@example.com')).toBeInTheDocument()
    expect(screen.getByText('Plan: plus')).toBeInTheDocument()
    expect(screen.queryByText('acc-1')).not.toBeInTheDocument()
    expect(screen.queryByText(/ai-lb codex/)).not.toBeInTheDocument()
    expect(screen.getByText(/used 30%/)).toBeInTheDocument()

    await user.type(screen.getByRole('textbox', { name: 'Working directory' }), '/tmp/project with spaces')
    await user.click(screen.getByRole('button', { name: 'Launch Codex CLI' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Codex CLI launched.')
    expect(calls.some((c) => c.includes('/codex/launch'))).toBe(true)

    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(calls.some((c) => c.includes('/codex/refresh'))).toBe(true)

    await user.click(screen.getByRole('button', { name: 'Disconnect' }))
    await user.click(await screen.findByRole('button', { name: 'Confirm disconnect' }))
    expect(await screen.findByText('Not connected')).toBeInTheDocument()
    expect(changed).toBe(1)
  })

  it('shows launch errors', async () => {
    const user = userEvent.setup()
    routes = [
      {
        method: 'GET',
        match: (u) => u.endsWith('/codex/status'),
        respond: () => Response.json({ connected: true, requires_openai_auth: true, observed_at: '' }),
      },
      {
        method: 'POST',
        match: (u) => u.endsWith('/codex/launch'),
        respond: () => new Response(JSON.stringify({ error: 'terminal_unavailable' }), { status: 503 }),
      },
    ]
    render(<CodexAccount account={profile} onChanged={() => undefined} />)
    await user.click(await screen.findByRole('button', { name: 'Launch Codex CLI' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('terminal_unavailable')
  })

  it('shows provider errors without crashing', async () => {
    routes = [
      {
        method: 'GET',
        match: (u) => u.endsWith('/codex/status'),
        respond: () =>
          new Response(JSON.stringify({ error: 'codex_not_installed' }), { status: 503 }),
      },
    ]
    // fetchCodexStatus throws on non-2xx only via check(); craft the
    // failure through a rejected status code path.
    render(<CodexAccount account={profile} onChanged={() => undefined} />)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
  })
})

describe('CodexAccount duplicate', () => {
  beforeEach(() => {
    routes = []
    calls = []
    vi.stubGlobal('fetch', mockFetch())
  })

  it('shows the duplicate message with the existing profile label', async () => {
    const user = userEvent.setup()
    routes.push(
      {
        method: 'GET',
        match: (u) => u.endsWith('/codex/status'),
        respond: () => Response.json({ connected: false, requires_openai_auth: true, observed_at: '' }),
      },
      {
        method: 'POST',
        match: (u) => u.endsWith('/codex/login'),
        respond: () =>
          Response.json({
            account_id: 'acc-1',
            login_id: 'login-9',
            method: 'browser',
            state: 'waiting',
            auth_url: 'https://example.com/auth',
          }),
      },
      {
        method: 'GET',
        match: (u) => u.endsWith('/codex/login'),
        respond: () =>
          Response.json({
            account_id: 'acc-1',
            login_id: 'login-9',
            method: 'browser',
            state: 'failed',
            error_code: 'duplicate_provider_account',
            error: 'This Codex account is already connected as "Personal".',
          }),
      },
    )
    render(<CodexAccount account={profile} onChanged={() => undefined} />)
    await user.click(await screen.findByRole('button', { name: 'Connect with ChatGPT' }))
    expect(
      await screen.findByText('This Codex account is already connected as "Personal".', {}, { timeout: 8000 }),
    ).toBeInTheDocument()
  }, 15000)
})
