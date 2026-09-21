export interface Settings {
  control_host: string
  control_port: number
  gateway_host: string
  gateway_port: number
  update_channel: string
}

export interface AppInfo {
  version: string
  control: { host: string; port: string; url: string }
  gateway: { host: string; port: string; url: string }
  database: { path: string; status: string }
  platform: { os: string; arch: string }
}

export interface GatewayStatus {
  url: string
  reachable: boolean
  status?: string
  error?: string
}

export interface UpdateState {
  status: string
  channel: string
  current_version: string
  available_version?: string
  available_channel?: string
  release_url?: string
  asset?: { name: string; url: string; size: number }
  asset_note?: string
  published_at?: string
  error?: string
  checked_at?: string
}

export interface DownloadResult {
  status: string
  path: string
  sha256: string
  size: number
}

export interface SettingsUpdate {
  settings: Settings
  restart_required: boolean
}

async function check(res: Response): Promise<never | Response> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error((body as { error?: string }).error ?? `request failed: ${res.status}`)
  }
  return res
}

export async function fetchApp(): Promise<AppInfo> {
  const res = await check(await fetch('/api/app'))
  return (await res.json()) as AppInfo
}

export async function fetchSettings(): Promise<Settings> {
  const res = await check(await fetch('/api/settings'))
  return (await res.json()) as Settings
}

export async function saveSettings(s: Settings): Promise<SettingsUpdate> {
  const res = await check(
    await fetch('/api/settings', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(s),
    }),
  )
  return (await res.json()) as SettingsUpdate
}

export async function fetchGateway(): Promise<GatewayStatus> {
  const res = await check(await fetch('/api/gateway'))
  return (await res.json()) as GatewayStatus
}

export async function fetchUpdate(): Promise<UpdateState> {
  const res = await check(await fetch('/api/update'))
  return (await res.json()) as UpdateState
}

export async function postUpdateCheck(): Promise<UpdateState> {
  const res = await check(await fetch('/api/update/check', { method: 'POST' }))
  return (await res.json()) as UpdateState
}

export async function postUpdateDownload(): Promise<DownloadResult> {
  const res = await check(await fetch('/api/update/download', { method: 'POST' }))
  return (await res.json()) as DownloadResult
}

export interface Provider {
  id: string
  display_name: string
  implemented: boolean
}

export interface Account {
  id: string
  provider: string
  label: string
  identity: string
  enabled: boolean
  connected: boolean
  created_at: string
  updated_at: string
}

export interface CreateAccountRequest {
  provider: string
  label: string
  identity?: string
  enabled?: boolean
}

export interface UpdateAccountRequest {
  label?: string
  identity?: string
  enabled?: boolean
}

export async function fetchProviders(): Promise<Provider[]> {
  const res = await check(await fetch('/api/providers'))
  const body = (await res.json()) as { providers: Provider[] }
  return body.providers ?? []
}

export async function fetchAccounts(): Promise<Account[]> {
  const res = await check(await fetch('/api/accounts'))
  const body = (await res.json()) as { accounts: Account[] }
  return body.accounts ?? []
}

export async function createAccount(req: CreateAccountRequest): Promise<Account> {
  const res = await check(
    await fetch('/api/accounts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),
  )
  return (await res.json()) as Account
}

export async function updateAccount(id: string, req: UpdateAccountRequest): Promise<Account> {
  const res = await check(
    await fetch(`/api/accounts/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),
  )
  return (await res.json()) as Account
}

export async function deleteAccount(id: string): Promise<void> {
  const res = await fetch(`/api/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (res.status !== 204) await check(res)
}

export interface CodexProviderStatus {
  installed: boolean
  path?: string
  version?: string
  app_server_compatible: boolean
  error?: string
}

export interface CodexQuotaWindow {
  limit_id?: string
  limit_name?: string
  used_percent?: number
  remaining_percent?: number
  window_duration_minutes?: number
  reset_at?: number
}

export interface CodexQuota {
  windows: CodexQuotaWindow[]
  updated_at: string
  stale: boolean
}

export interface CodexStatus {
  connected: boolean
  auth_mode?: string
  email?: string
  plan_type?: string
  requires_openai_auth: boolean
  quota?: CodexQuota
  observed_at: string
}

export interface CodexLogin {
  account_id: string
  login_id: string
  method: string
  state: string
  auth_url?: string
  verification_url?: string
  user_code?: string
  error?: string
  error_code?: string
  started_at: string
}

export async function fetchCodexProviderStatus(): Promise<CodexProviderStatus> {
  const res = await check(await fetch('/api/providers/codex/status'))
  return (await res.json()) as CodexProviderStatus
}

export async function startCodexLogin(id: string, method: 'browser' | 'device'): Promise<CodexLogin> {
  const res = await check(
    await fetch(`/api/accounts/${encodeURIComponent(id)}/codex/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method }),
    }),
  )
  return (await res.json()) as CodexLogin
}

export async function fetchCodexLogin(id: string): Promise<CodexLogin> {
  const res = await check(await fetch(`/api/accounts/${encodeURIComponent(id)}/codex/login`))
  return (await res.json()) as CodexLogin
}

export async function cancelCodexLogin(id: string, loginId?: string): Promise<CodexLogin> {
  const res = await check(
    await fetch(`/api/accounts/${encodeURIComponent(id)}/codex/login`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(loginId ? { login_id: loginId } : {}),
    }),
  )
  return (await res.json()) as CodexLogin
}

export async function fetchCodexStatus(id: string): Promise<CodexStatus> {
  const res = await check(await fetch(`/api/accounts/${encodeURIComponent(id)}/codex/status`))
  return (await res.json()) as CodexStatus
}

export async function refreshCodexQuota(id: string): Promise<CodexQuota> {
  const res = await check(
    await fetch(`/api/accounts/${encodeURIComponent(id)}/codex/refresh`, { method: 'POST' }),
  )
  return (await res.json()) as CodexQuota
}

export async function logoutCodex(id: string): Promise<void> {
  await check(
    await fetch(`/api/accounts/${encodeURIComponent(id)}/codex/logout`, { method: 'POST' }),
  )
}
