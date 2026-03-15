import type { BelayEvent, BelaySession, BelayConflict, FileInfo, Stats, HealthData } from '../types'
const API_PORT = 33412

function getApiBase(): string {
  if (typeof window !== 'undefined' && 'Capacitor' in window) {
    return `https://${window.location.hostname}:${API_PORT + 10000}/api`
  }
  try {
    const isDev = (import.meta as unknown as Record<string, Record<string, boolean>>).env?.DEV
    if (isDev) return '/api'
  } catch {}
  return `https://${window.location.hostname}:${API_PORT + 10000}/api`
}

const BASE = getApiBase()

async function fetchJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`)
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

export async function getHealth(): Promise<HealthData> {
  return fetchJSON('/health')
}

export async function getStats(): Promise<Stats> {
  return fetchJSON('/stats')
}

export async function getEvents(params?: {
  since?: string
  until?: string
  file?: string
  session?: string
  limit?: number
  order?: 'asc' | 'desc'
  attribution?: string
}): Promise<{ events: BelayEvent[]; count: number }> {
  const qs = new URLSearchParams()
  if (params?.since) qs.set('since', params.since)
  if (params?.until) qs.set('until', params.until)
  if (params?.file) qs.set('file', params.file)
  if (params?.session) qs.set('session', params.session)
  if (params?.limit) qs.set('limit', String(params.limit))
  if (params?.order) qs.set('order', params.order)
  if (params?.attribution) qs.set('attribution', params.attribution)
  const q = qs.toString()
  return fetchJSON(`/events${q ? `?${q}` : ''}`)
}

export async function getEvent(id: string): Promise<BelayEvent> {
  return fetchJSON(`/events/${id}`)
}

export async function getSessions(activeOnly = false): Promise<{ sessions: BelaySession[]; count: number }> {
  const params = new URLSearchParams()
  if (activeOnly) params.set('active', 'true')
  params.set('min_events', '1')
  return fetchJSON(`/sessions?${params}`)
}

export async function getSession(id: string): Promise<BelaySession> {
  return fetchJSON(`/sessions/${id}`)
}

export async function getSessionEvents(id: string, limit?: number): Promise<{ events: BelayEvent[]; count: number }> {
  const qs = limit ? `?limit=${limit}` : ''
  return fetchJSON(`/sessions/${id}/events${qs}`)
}

export async function getSessionReplay(id: string): Promise<Record<string, unknown>> {
  return fetchJSON(`/sessions/${id}/replay`)
}

export async function getFiles(since?: string, attribution?: string): Promise<{ files: FileInfo[]; count: number }> {
  const qs = new URLSearchParams()
  if (since) qs.set('since', since)
  if (attribution) qs.set('attribution', attribution)
  const q = qs.toString()
  return fetchJSON(`/files${q ? `?${q}` : ''}`)
}

export async function getFileHistory(path: string, limit?: number): Promise<{ events: BelayEvent[]; count: number }> {
  const qs = new URLSearchParams({ path })
  if (limit) qs.set('limit', String(limit))
  return fetchJSON(`/files/history?${qs}`)
}

export async function getFileContent(hash: string): Promise<string> {
  const res = await fetch(`${BASE}/files/content?hash=${hash}`)
  if (!res.ok) throw new Error(res.statusText)
  return res.text()
}

export async function getConflicts(params?: {
  since?: string
  file?: string
}): Promise<{ conflicts: BelayConflict[]; count: number }> {
  const qs = new URLSearchParams()
  if (params?.since) qs.set('since', params.since)
  if (params?.file) qs.set('file', params.file)
  const q = qs.toString()
  return fetchJSON(`/conflicts${q ? `?${q}` : ''}`)
}

export function connectStream(onMessage: (msg: Record<string, unknown>) => void): EventSource {
  const es = new EventSource(`${BASE}/stream`)
  es.onmessage = (e) => {
    try {
      const data = JSON.parse(e.data)
      onMessage(data)
    } catch {
    }
  }
  return es
}
