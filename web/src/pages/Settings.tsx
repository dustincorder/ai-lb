import { useEffect, useState } from 'react'
import { fetchSettings, saveSettings, type Settings } from '../api'
import { Alert, Badge, Button, Card, PageHeader, Spinner } from '../components'

function parsePort(raw: string): number | null { if (!/^\d+$/.test(raw.trim())) return null; const n = Number(raw); return Number.isInteger(n) && n >= 1 && n <= 65535 ? n : null }

export function SettingsPage() {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [controlPort, setControlPort] = useState('')
  const [gatewayPort, setGatewayPort] = useState('')
  const [channel, setChannel] = useState('stable')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  useEffect(() => { fetchSettings().then((s) => { setSettings(s); setControlPort(String(s.control_port)); setGatewayPort(String(s.gateway_port)); setChannel(s.update_channel || 'stable') }).catch((e: Error) => setError(e.message)) }, [])
  if (error && !settings) return <><PageHeader eyebrow="Configuration" title="Settings" description="Tune the local service without leaving the browser." /><Alert>{error}</Alert></>
  if (!settings) return <div className="loading-block"><Spinner label="Loading settings" /></div>
  async function onSave() { if (!settings) return; setError(null); setNotice(null); const cp = parsePort(controlPort); const gp = parsePort(gatewayPort); if (cp === null || gp === null) { setError('Ports must be integers in range 1..65535.'); return }; setSaving(true); try { const res = await saveSettings({ control_host: settings.control_host, control_port: cp, gateway_host: settings.gateway_host, gateway_port: gp, update_channel: channel }); setSettings(res.settings); setNotice(res.restart_required ? 'Settings saved. Restart the service for new ports to take effect.' : 'Settings saved.') } catch (e) { setError(e instanceof Error ? e.message : 'Save failed.') } finally { setSaving(false) } }
  return <><PageHeader eyebrow="Configuration" title="Settings" description="Tune the local service without leaving the browser." />{error && <Alert>{error}</Alert>}{notice && <Alert tone="success">{notice}</Alert>}<div className="section"><Card><div className="section-title"><h2>Network</h2><Badge>Loopback only</Badge></div><div className="form-grid"><div className="field"><label htmlFor="control-host">Control host</label><input id="control-host" value={settings.control_host} readOnly /></div><div className="field"><label htmlFor="control-port">Control port</label><input id="control-port" inputMode="numeric" value={controlPort} onChange={(e) => setControlPort(e.target.value)} /></div><div className="field"><label htmlFor="gateway-host">Gateway host</label><input id="gateway-host" value={settings.gateway_host} readOnly /></div><div className="field"><label htmlFor="gateway-port">Gateway port</label><input id="gateway-port" inputMode="numeric" value={gatewayPort} onChange={(e) => setGatewayPort(e.target.value)} /></div></div></Card></div><div className="section"><Card><div className="section-title"><h2>Updates</h2></div><div className="field"><label htmlFor="update-channel">Update channel</label><select id="update-channel" value={channel} onChange={(e) => setChannel(e.target.value)}><option value="stable">Stable - tested releases</option><option value="nightly">Nightly - latest development builds</option></select></div><div className="account-actions section"><Button onClick={() => void onSave()} disabled={saving}>{saving ? 'Saving...' : 'Save changes'}</Button></div></Card></div></>
}
