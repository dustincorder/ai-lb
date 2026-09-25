import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

  it('saves settings and displays restart notice', async () => {
    const user = userEvent.setup()
    vi.stubGlobal(
      'fetch',
      vi.fn((_url: string, init?: RequestInit) => {
        if (init?.method === 'PUT') {
          return Promise.resolve(
            Response.json({
              settings: {
                control_host: '127.0.0.1',
                control_port: 8400,
                gateway_host: '127.0.0.1',
                gateway_port: 8401,
                update_channel: 'nightly',
              },
              restart_required: true,
            }),
          )
        }
        return Promise.resolve(
          Response.json({
            control_host: '127.0.0.1',
            control_port: 8317,
            gateway_host: '127.0.0.1',
            gateway_port: 8318,
            update_channel: 'stable',
          }),
        )
      }),
    )

    render(<SettingsPage />)
    const control = (await screen.findByLabelText('Control port')) as HTMLInputElement
    await user.clear(control)
    await user.type(control, '8400')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    expect(
      await screen.findByText(/Settings saved. Restart the service for new ports to take effect./),
    ).toBeInTheDocument()
  })
})
