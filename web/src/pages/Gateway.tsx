import { useEffect, useState } from 'react'
import { fetchApp, fetchGateway, type AppInfo, type GatewayStatus } from '../api'
import { Alert, Badge, Card, CopyButton, PageHeader, Spinner } from '../components'

export function Gateway() {
  const [app, setApp] = useState<AppInfo | null>(null); const [status, setStatus] = useState<GatewayStatus | null>(null); const [error, setError] = useState<string | null>(null)
  useEffect(() => { Promise.all([fetchApp(), fetchGateway()]).then(([a, g]) => { setApp(a); setStatus(g) }).catch((e: Error) => setError(e.message)) }, [])
  return <><PageHeader eyebrow="Network" title="Gateway" description="The local gateway is health-only today. OpenAI-compatible proxying is not implemented yet." />{error && <Alert>{error}</Alert>}{!app && !error && <div className="loading-block"><Spinner label="Loading gateway" /></div>}{app && status && <Card><div className="status-line"><div><div className="metric-label">Gateway listener</div><div className="metric-value">{status.reachable ? 'Running' : 'Unavailable'}</div></div><Badge tone={status.reachable ? 'success' : 'danger'}>{status.reachable ? 'Healthy' : 'Offline'}</Badge></div><div className="technical section">{app.gateway.url}</div><div className="account-actions section"><CopyButton value={app.gateway.url} /></div></Card>}<Card className="section"><div className="metric-label">API proxy</div><h2>Not implemented yet</h2><p className="metric-detail">Load balancing and OpenAI-compatible endpoints are planned. This page reports the listener honestly.</p></Card></>
}
