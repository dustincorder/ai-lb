import { useEffect, useState } from 'react'
import { fetchSettings, saveSettings, type Settings } from '../api'

function parsePort(raw: string): number | null {
  if (!/^\d+$/.test(raw.trim())) return null
  const n = Number(raw)
  if (!Number.isInteger(n) || n < 1 || n > 65535) return null
  return n
}

export function SettingsPage() {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [controlPort, setControlPort] = useState('')
  const [gatewayPort, setGatewayPort] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    fetchSettings()
      .then((s) => {
        setSettings(s)
        setControlPort(String(s.control_port))
        setGatewayPort(String(s.gateway_port))
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  if (error) return <p role="alert">Failed to load settings: {error}</p>
  if (!settings) return <p>Loading…</p>

  async function onSave() {
    setError(null)
    setNotice(null)
    if (!settings) {
      setError('Settings are still loading.')
      return
    }
    const cp = parsePort(controlPort)
    const gp = parsePort(gatewayPort)
    if (cp === null || gp === null) {
      setError('Ports must be integers in range 1..65535.')
      return
    }
    setSaving(true)
    try {
      const next: Settings = {
        control_host: settings.control_host,
        control_port: cp,
        gateway_host: settings.gateway_host,
        gateway_port: gp,
      }
      const res = await saveSettings(next)
      setSettings(res.settings)
      setNotice(
        res.restart_required
          ? 'Settings saved. Restart the service for the new ports to take effect.'
          : 'Settings saved. No restart needed.',
      )
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section>
      <h2>Settings</h2>
      <p>
        Hosts are loopback-only and not editable. Changing a port requires a
        service restart to take effect.
      </p>
      <div>
        <label htmlFor="control-port">Control port</label>
        <input
          id="control-port"
          inputMode="numeric"
          value={controlPort}
          onChange={(e) => setControlPort(e.target.value)}
        />
      </div>
      <div>
        <label htmlFor="gateway-port">Gateway port</label>
        <input
          id="gateway-port"
          inputMode="numeric"
          value={gatewayPort}
          onChange={(e) => setGatewayPort(e.target.value)}
        />
      </div>
      <button type="button" onClick={onSave} disabled={saving}>
        {saving ? 'Saving…' : 'Save'}
      </button>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
    </section>
  )
}
