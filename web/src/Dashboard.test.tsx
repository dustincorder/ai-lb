import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Dashboard } from './pages/Dashboard'

function mockFetch() {
  return vi.fn(async (url: string) => {
    if (url === '/api/app') {
      return Response.json({
        data_dir: '/var/local/ai-lb-data',
        version: 'v0.1.0',
        control: { host: '127.0.0.1', port: '8317', url: 'http://127.0.0.1:8317' },
        gateway: { host: '127.0.0.1', port: '8318', url: 'http://127.0.0.1:8318' },
        database: { path: '/var/local/ai-lb-data/ai-lb.db', status: 'ready' },
        platform: { os: 'linux', arch: 'amd64' },
      })
    }
    if (url === '/api/gateway') {
      return Response.json({
        url: 'http://127.0.0.1:8318',
        reachable: true,
      })
    }
    if (url === '/api/accounts') {
      return Response.json({
        accounts: [
          {
            id: 'acc-1',
            provider: 'codex',
            label: 'Personal',
            identity: 'p@example.com',
            enabled: true,
            connected: true,
          },
          {
            id: 'acc-2',
            provider: 'codex',
            label: 'Work',
            identity: '',
            enabled: true,
            connected: false,
          },
        ],
      })
    }
    if (url === '/api/providers') {
      return Response.json({
        providers: [
          { id: 'codex', display_name: 'Codex', implemented: true },
          { id: 'antigravity', display_name: 'Antigravity', implemented: false },
        ],
      })
    }
    return new Response('{}', { status: 200 })
  })
}

describe('Dashboard', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', mockFetch())
  })

  it('renders service overview metrics and provider statuses', async () => {
    render(<Dashboard />)
    expect(await screen.findByRole('heading', { name: 'Dashboard' })).toBeInTheDocument()
    expect(screen.getAllByText('Running').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText(/v0.1.0 · linux\/amd64/)).toBeInTheDocument()

    // Accounts metrics
    expect(screen.getByText('2')).toBeInTheDocument()
    expect(screen.getByText('1 connected profiles')).toBeInTheDocument()

    // Gateway status
    expect(screen.getByText('http://127.0.0.1:8318')).toBeInTheDocument()
    expect(screen.getByText('http://127.0.0.1:8317')).toBeInTheDocument()

    // Providers
    expect(screen.getByText('Integrated')).toBeInTheDocument()
    expect(screen.getByText('Planned')).toBeInTheDocument()
    expect(screen.getByText('Coming soon')).toBeInTheDocument()

    // Local data
    expect(screen.getByText('ready')).toBeInTheDocument()
    expect(screen.getByText('/var/local/ai-lb-data')).toBeInTheDocument()
  })

  it('handles offline gateway reporting', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url === '/api/gateway') {
          return Response.json({ url: 'http://127.0.0.1:8318', reachable: false })
        }
        return mockFetch()(url)
      }),
    )

    render(<Dashboard />)
    expect(await screen.findByText('Unavailable')).toBeInTheDocument()
    expect(screen.getByText('Offline')).toBeInTheDocument()
  })
})
