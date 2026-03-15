import { create } from 'zustand'
import type { BelayEvent, BelaySession, Stats, HealthData } from '../types'
import * as api from '../services/api'

interface BelayState {
  stats: Stats | null
  events: BelayEvent[]
  sessions: BelaySession[]
  connected: boolean
  health: HealthData | null

  loadingStats: boolean
  loadingEvents: boolean
  loadingSessions: boolean

  statsError: string | null
  eventsError: string | null
  sessionsError: string | null

  fetchStats: () => Promise<void>
  fetchEvents: (params?: Parameters<typeof api.getEvents>[0]) => Promise<void>
  fetchSessions: (activeOnly?: boolean) => Promise<void>
  fetchHealth: () => Promise<void>
  setConnected: (v: boolean) => void
  addLiveEvent: (event: BelayEvent) => void
}

export function isWatcherHealthy(health: HealthData | null): boolean {
  if (!health) return true
  if (!health.watcher) return true
  return health.watcher.status === 'running'
}

export function isWatcherDegraded(health: HealthData | null): boolean {
  if (!health?.watcher) return false
  return health.watcher.status === 'degraded'
}

export function getWatcherSeverity(health: HealthData | null): 'ok' | 'warning' | 'critical' {
  if (!health || !health.watcher) return 'ok'
  if (health.watcher.status === 'running') return 'ok'
  if (health.watcher.status === 'degraded') return 'warning'
  if (!health.watcher.last_event_at) return 'critical'
  const lastEventMs = new Date(health.watcher.last_event_at).getTime()
  const fiveMinAgo = Date.now() - 5 * 60 * 1000
  if (lastEventMs > fiveMinAgo) return 'warning'
  return 'critical'
}

export function formatTimeSince(isoDate: string): string {
  const ms = Date.now() - new Date(isoDate).getTime()
  if (ms < 0) return 'just now'
  const seconds = Math.floor(ms / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

export const useBelayStore = create<BelayState>((set) => ({
  stats: null,
  events: [],
  sessions: [],
  connected: false,
  health: null,
  loadingStats: false,
  loadingEvents: false,
  loadingSessions: false,
  statsError: null,
  eventsError: null,
  sessionsError: null,

  fetchHealth: async () => {
    try {
      const health = await api.getHealth()
      set({ health })
    } catch {
      set({ health: null })
    }
  },

  fetchStats: async () => {
    set({ loadingStats: true, statsError: null })
    try {
      const stats = await api.getStats()
      set({ stats, loadingStats: false })
    } catch (err) {
      set({ loadingStats: false, statsError: err instanceof Error ? err.message : String(err) })
    }
  },

  fetchEvents: async (params) => {
    set({ loadingEvents: true, eventsError: null })
    try {
      const { events } = await api.getEvents(params)
      set({ events: events ?? [], loadingEvents: false })
    } catch (err) {
      set({ loadingEvents: false, eventsError: err instanceof Error ? err.message : String(err) })
    }
  },

  fetchSessions: async (activeOnly = false) => {
    set({ loadingSessions: true, sessionsError: null })
    try {
      const { sessions } = await api.getSessions(activeOnly)
      set({ sessions: sessions ?? [], loadingSessions: false })
    } catch (err) {
      set({ loadingSessions: false, sessionsError: err instanceof Error ? err.message : String(err) })
    }
  },

  setConnected: (connected) => set({ connected }),

  addLiveEvent: (event) =>
    set((state) => ({
      events: [event, ...state.events].slice(0, 200),
    })),
}))
