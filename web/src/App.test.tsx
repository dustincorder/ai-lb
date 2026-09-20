import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'

function mockFetch() {
  return vi.fn((url: string) => {
    if (typeof url === 'string' && url.endsWith('/api/app')) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            version: 'test',
            control: { host: '127.0.0.1', port: '8317', url: 'http://127.0.0.1:8317' },
            gateway: { host: '127.0.0.1', port: '8318', url: 'http://127.0.0.1:8318' },
            database: { path: '/tmp/ai-lb.db', status: 'ok' },
            platform: { os: 'linux', arch: 'amd64' },
          }),
          { status: 200 },
        ),
      )
    }
    return Promise.resolve(new Response(JSON.stringify({}), { status: 200 }))
  })
}

describe('App', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', mockFetch())
  })

  it('renders the dashboard by default', async () => {
    render(<App />)
    expect(await screen.findByRole('heading', { name: 'Dashboard' })).toBeInTheDocument()
    expect(await screen.findByText('http://127.0.0.1:8317')).toBeInTheDocument()
  })

  it('navigates between pages', async () => {
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Dashboard' })

    await user.click(screen.getByRole('button', { name: 'Accounts' }))
    expect(await screen.findByRole('heading', { name: 'Accounts' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Gateway' }))
    expect(await screen.findByRole('heading', { name: 'Gateway' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Settings' }))
    expect(await screen.findByRole('heading', { name: 'Settings' })).toBeInTheDocument()
  })
})
