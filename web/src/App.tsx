import { useEffect, useState } from 'react'
import { Accounts } from './pages/Accounts'
import { Dashboard } from './pages/Dashboard'
import { Gateway } from './pages/Gateway'
import { SettingsPage } from './pages/Settings'
import { UpdateBanner } from './UpdateBanner'
import { fetchUpdate, type UpdateState } from './api'

const tabs = ['Dashboard', 'Accounts', 'Gateway', 'Settings'] as const
type Tab = (typeof tabs)[number]

export function App() {
  const [tab, setTab] = useState<Tab>('Dashboard')
  const [update, setUpdate] = useState<UpdateState | null>(null)

  useEffect(() => {
    fetchUpdate().then(setUpdate).catch(() => setUpdate(null))
  }, [])

  return (
    <div>
      <header>
        <h1>ai-lb</h1>
        <nav>
          {tabs.map((t) => (
            <button
              key={t}
              type="button"
              aria-current={tab === t ? 'page' : undefined}
              onClick={() => setTab(t)}
            >
              {t}
            </button>
          ))}
        </nav>
      </header>
      {update && <UpdateBanner initial={update} />}
      <main>
        {tab === 'Dashboard' && <Dashboard />}
        {tab === 'Accounts' && <Accounts />}
        {tab === 'Gateway' && <Gateway />}
        {tab === 'Settings' && <SettingsPage />}
      </main>
    </div>
  )
}
