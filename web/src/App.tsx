import { useEffect, useState } from 'react'
import { Accounts } from './pages/Accounts'
import { Dashboard } from './pages/Dashboard'
import { Gateway } from './pages/Gateway'
import { SettingsPage } from './pages/Settings'
import { UpdateBanner } from './UpdateBanner'
import { fetchUpdate, type UpdateState } from './api'
import { Button } from './components'

const tabs = ['Dashboard', 'Accounts', 'Gateway', 'Settings'] as const
type Tab = (typeof tabs)[number]

export function App() {
  const [tab, setTab] = useState<Tab>('Dashboard')
  const [update, setUpdate] = useState<UpdateState | null>(null)
  const [theme, setTheme] = useState(() => { try { return window.localStorage?.getItem('ai-lb-theme') || 'system' } catch { return 'system' } })

  useEffect(() => { fetchUpdate().then(setUpdate).catch(() => setUpdate(null)) }, [])
  useEffect(() => {
    if (theme === 'system') document.documentElement.removeAttribute('data-theme')
    else document.documentElement.dataset.theme = theme
    try { window.localStorage?.setItem('ai-lb-theme', theme) } catch { /* storage is optional */ }
  }, [theme])

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand"><div className="brand-mark">a</div><div><strong>ai-lb</strong><span>local AI control</span></div></div>
      <nav aria-label="Main navigation" className="sidebar-nav">
        <p className="nav-label">Workspace</p>
        {tabs.map((item) => <button key={item} type="button" className={`nav-item ${tab === item ? 'active' : ''}`} aria-current={tab === item ? 'page' : undefined} onClick={() => setTab(item)}><span className="nav-icon" aria-hidden="true">{item === 'Dashboard' ? '[]' : item === 'Accounts' ? '@' : item === 'Gateway' ? '<>' : '*'}</span>{item}</button>)}
      </nav>
      <div className="sidebar-footer"><span className="version-chip">v0.1 dev</span><span>Local-first. Private by default.</span><Button variant="ghost" onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}>{theme === 'light' ? 'Dark mode' : 'Light mode'}</Button></div>
    </aside>
    <div className="main-column">
      <header className="mobile-header"><div className="brand"><div className="brand-mark">a</div><strong>ai-lb</strong></div><select aria-label="Navigate" value={tab} onChange={(e) => setTab(e.target.value as Tab)}>{tabs.map((item) => <option key={item}>{item}</option>)}</select></header>
      <div className="topbar"><span className="live-dot" /> Service online <span className="topbar-separator" /> localhost</div>
      {update && <UpdateBanner initial={update} />}
      <main className="content">{tab === 'Dashboard' && <Dashboard />}{tab === 'Accounts' && <Accounts />}{tab === 'Gateway' && <Gateway />}{tab === 'Settings' && <SettingsPage />}</main>
    </div>
  </div>
}
