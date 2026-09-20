import { useEffect, useState } from 'react'
import { fetchApp, fetchGateway, type GatewayStatus } from '../api'

export function Gateway() {
  const [gatewayUrl, setGatewayUrl] = useState<string>('')
  const [status, setStatus] = useState<GatewayStatus | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchApp()
      .then((app) => {
        setGatewayUrl(app.gateway.url)
        return fetchGateway()
      })
      .then(setStatus)
      .catch((e: Error) => setError(e.message))
  }, [])

  return (
    <section>
      <h2>Gateway</h2>
      {error && <p role="alert">Failed to load gateway status: {error}</p>}
      {!error && !status && <p>Loading…</p>}
      {gatewayUrl && (
        <dl>
          <dt>URL</dt>
          <dd>{gatewayUrl}</dd>
        </dl>
      )}
      {status && (
        <dl>
          <dt>Status</dt>
          <dd>{status.reachable ? (status.status ?? 'reachable') : 'unreachable'}</dd>
          {!status.reachable && status.error && (
            <>
              <dt>Error</dt>
              <dd>{status.error}</dd>
            </>
          )}
        </dl>
      )}
    </section>
  )
}
