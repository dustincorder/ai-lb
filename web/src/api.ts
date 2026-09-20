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
