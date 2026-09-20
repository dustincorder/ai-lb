import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { SettingsPage } from './pages/Settings'

describe('SettingsPage', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response(
            JSON.stringify({
              control_host: '127.0.0.1',
              control_port: 8317,
              gateway_host: '127.0.0.1',
              gateway_port: 8318,
            }),
            { status: 200 },
          ),
        ),
      ),
    )
  })

  it('renders current ports from the API', async () => {
    render(<SettingsPage />)
    const control = (await screen.findByLabelText('Control port')) as HTMLInputElement
    const gateway = screen.getByLabelText('Gateway port') as HTMLInputElement
    expect(control.value).toBe('8317')
    expect(gateway.value).toBe('8318')
  })

  it('defaults the update channel to stable when unset', async () => {
    render(<SettingsPage />)
    const channel = (await screen.findByLabelText('Update channel')) as HTMLSelectElement
    expect(channel.value).toBe('stable')
  })
})
