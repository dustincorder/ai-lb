import { useEffect, useState } from 'react'
import { fetchApp, type AppInfo } from '../api'

export function Dashboard() {
  const [app, setApp] = useState<AppInfo | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchApp().then(setApp).catch((e: Error) => setError(e.message))
  }, [])

  if (error) return <p role="alert">Failed to load service info: {error}</p>
  if (!app) return <p>Loading…</p>

  return (
    <section>
      <h2>Dashboard</h2>
      <dl>
        <dt>Version</dt>
        <dd>{app.version}</dd>
        <dt>Control</dt>
        <dd>{app.control.url}</dd>
        <dt>Gateway</dt>
        <dd>{app.gateway.url}</dd>
        <dt>Database</dt>
        <dd>
          {app.database.status} ({app.database.path})
        </dd>
        <dt>Platform</dt>
        <dd>
          {app.platform.os}/{app.platform.arch}
        </dd>
      </dl>
    </section>
  )
}
