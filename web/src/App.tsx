import { useState } from 'react'
import { Accounts } from './pages/Accounts'
import { Dashboard } from './pages/Dashboard'
import { Gateway } from './pages/Gateway'
import { SettingsPage } from './pages/Settings'

const tabs = ['Dashboard', 'Accounts', 'Gateway', 'Settings'] as const
type Tab = (typeof tabs)[number]

export function App() {
  const [tab, setTab] = useState<Tab>('Dashboard')

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
      <main>
        {tab === 'Dashboard' && <Dashboard />}
        {tab === 'Accounts' && <Accounts />}
        {tab === 'Gateway' && <Gateway />}
        {tab === 'Settings' && <SettingsPage />}
      </main>
    </div>
  )
}
