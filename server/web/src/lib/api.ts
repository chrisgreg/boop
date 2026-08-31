// Thin typed client for the Boop API. Admin endpoints are unauthenticated in v1.

export type Level = 'info' | 'success' | 'warning' | 'error' | 'critical'
export const LEVELS: Level[] = ['info', 'success', 'warning', 'error', 'critical']

export interface Project {
  id: string
  name: string
  slug: string
  icon: string
  notify: boolean
  min_level: Level
  created_at: string
  updated_at: string
}

export interface ProjectCreated extends Project {
  api_key: string
}

/** A button on the notification and in the event detail that opens a URL. */
export interface EventAction {
  label: string
  url: string
}

/** Present on grouped listings: this row stands for `count` occurrences of its fingerprint. */
export interface GroupInfo {
  count: number
  first_seen: string
  last_seen: string
}

export interface Event {
  id: string
  external_id?: string
  project_id: string
  project_name: string
  project_slug: string
  project_icon: string
  source: string
  type: string
  level: Level
  title: string
  body: string
  fingerprint: string
  data: Record<string, unknown>
  occurred_at: string
  created_at: string
  silenced: boolean
  silence_id?: string
  actions?: EventAction[]
  group?: GroupInfo
}

export type SilenceField = 'fingerprint' | 'title' | 'source'

export interface Silence {
  id: string
  project_id?: string
  project_name?: string
  field: SilenceField
  value: string
  note: string
  created_at: string
}

export interface EventPage {
  events: Event[]
  next_cursor?: string
}

export interface Device {
  id: string
  name: string
  push_registered: boolean
  platform: string
  app_bundle_id: string
  last_seen_at: string | null
  created_at: string
  updated_at: string
}

export interface Delivery {
  id: string
  event_id: string
  device_id?: string
  device_name?: string
  target_type: 'device' | 'webhook'
  webhook_id?: string
  webhook_host?: string
  status: 'sent' | 'failed' | 'skipped'
  apns_id?: string
  http_status?: number
  error?: string
  attempted_at: string
}

export type WebhookPayloadMode = 'json' | 'custom'

export interface Webhook {
  id: string
  project_id: string
  url: string
  payload_mode: WebhookPayloadMode
  body_template: string
  headers: Record<string, string>
  min_level: Level | ''
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface WebhookInput {
  url?: string
  payload_mode?: WebhookPayloadMode
  body_template?: string
  headers?: Record<string, string>
  min_level?: Level | ''
  enabled?: boolean
}

export interface PairingToken {
  id: string
  token?: string
  expires_at: string
  created_at: string
  qr?: { version: number; server: string; token: string }
}

export interface Status {
  version: string
  server: string
  database: string
  database_path: string
  base_url: string
  uptime_seconds: number
  apns: {
    configured: boolean
    error?: string
    missing?: string[]
    team_id?: string
    key_id?: string
    bundle_id?: string
    environment: string
  }
  devices: number
  pushable_devices: number
  projects: number
  events: number
  last_push: Delivery | null
  retention_days: number
  setup_completed: boolean
  admin_auth: boolean
}

export interface AuthState {
  auth_required: boolean
  authenticated: boolean
}

export interface Settings {
  retention_days: number
  redact_keys: string[]
  default_redact_keys: string[]
  setup_completed: boolean
  mcp_enabled: boolean
  mcp_token_set: boolean
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let parsed: any = null
  try {
    parsed = text ? JSON.parse(text) : null
  } catch {
    parsed = null
  }
  if (!res.ok) {
    if (res.status === 401 && parsed?.error === 'login_required') onLoginRequired?.()
    throw new ApiError(res.status, parsed?.error ?? 'error', parsed?.message ?? `Request failed (${res.status})`)
  }
  return parsed as T
}

/** Called on any 401 with login_required so the app can show the sign-in screen. */
export let onLoginRequired: (() => void) | null = null
export function setLoginHandler(fn: () => void) {
  onLoginRequired = fn
}

export const api = {
  me: () => request<AuthState>('GET', '/api/v1/auth/me'),
  login: (username: string, password: string) => request<AuthState>('POST', '/api/v1/auth/login', { username, password }),
  logout: () => request<void>('POST', '/api/v1/auth/logout'),

  status: () => request<Status>('GET', '/api/v1/status'),
  settings: () => request<Settings>('GET', '/api/v1/settings'),
  updateSettings: (patch: Partial<Settings>) => request<Settings>('PATCH', '/api/v1/settings', patch),

  events: (params: { project?: string; level?: string; source?: string; fingerprint?: string; before?: string; limit?: number; silenced?: string; grouped?: boolean } = {}) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '' && v !== false) q.set(k, String(v))
    const qs = q.toString()
    return request<EventPage>('GET', '/api/v1/events' + (qs ? '?' + qs : ''))
  },
  event: (id: string) => request<Event>('GET', `/api/v1/events/${encodeURIComponent(id)}`),
  eventDeliveries: (id: string) => request<{ deliveries: Delivery[] }>('GET', `/api/v1/events/${encodeURIComponent(id)}/deliveries`),

  projects: () => request<{ projects: Project[] }>('GET', '/api/v1/projects'),
  createProject: (input: { name: string; icon?: string }) => request<ProjectCreated>('POST', '/api/v1/projects', input),
  updateProject: (id: string, patch: Partial<Pick<Project, 'name' | 'icon' | 'notify' | 'min_level'>>) =>
    request<Project>('PATCH', `/api/v1/projects/${id}`, patch),
  deleteProject: (id: string) => request<void>('DELETE', `/api/v1/projects/${id}`),
  rotateKey: (id: string) => request<ProjectCreated>('POST', `/api/v1/projects/${id}/rotate-key`),
  webhooks: (projectId: string) => request<{ webhooks: Webhook[] }>('GET', `/api/v1/projects/${projectId}/webhooks`),
  createWebhook: (projectId: string, input: WebhookInput) => request<Webhook>('POST', `/api/v1/projects/${projectId}/webhooks`, input),
  updateWebhook: (projectId: string, id: string, patch: WebhookInput) => request<Webhook>('PATCH', `/api/v1/projects/${projectId}/webhooks/${id}`, patch),
  deleteWebhook: (projectId: string, id: string) => request<void>('DELETE', `/api/v1/projects/${projectId}/webhooks/${id}`),
  testWebhook: (projectId: string, id: string) => request<{ delivery: Delivery }>('POST', `/api/v1/projects/${projectId}/webhooks/${id}/test`),

  devices: () => request<{ devices: Device[] }>('GET', '/api/v1/devices'),
  updateDevice: (id: string, patch: { name?: string }) => request<Device>('PATCH', `/api/v1/devices/${id}`, patch),
  deleteDevice: (id: string) => request<void>('DELETE', `/api/v1/devices/${id}`),

  createPairing: () => request<PairingToken>('POST', '/api/v1/pairing'),
  pendingPairings: () => request<{ pairing_tokens: PairingToken[] }>('GET', '/api/v1/pairing'),
  revokePairing: (id: string) => request<void>('DELETE', `/api/v1/pairing/${id}`),

  silences: () => request<{ silences: Silence[]; fields: SilenceField[]; silenced_events: number }>('GET', '/api/v1/silences'),
  silence: (id: string) => request<Silence>('GET', `/api/v1/silences/${id}`),
  unsilence: (eventId: string) => request<{ event: Event; deliveries: Delivery[] }>('POST', `/api/v1/events/${encodeURIComponent(eventId)}/unsilence`),
  createSilence: (input: { field: SilenceField; value: string; project_id?: string; note?: string }) => request<Silence>('POST', '/api/v1/silences', input),
  deleteSilence: (id: string) => request<void>('DELETE', `/api/v1/silences/${id}`),

  test: (project_id?: string) =>
    request<{ event: Event; deliveries: Delivery[]; apns_configured: boolean }>('POST', '/api/v1/test', project_id ? { project_id } : {}),
}
