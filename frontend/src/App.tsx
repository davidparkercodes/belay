import { Routes, Route, Navigate, NavLink, useLocation } from 'react-router-dom'
import { Clock, List, Activity, GitFork, WarningTriangle, HardDrive, Flash, FlashOff, EyeClosed, Eye } from 'iconoir-react'
import { useEffect } from 'react'
import TimelineView from './views/TimelineView'
import SessionsView from './views/SessionsView'
import SessionDetailView from './views/SessionDetailView'
import FilesView from './views/FilesView'
import FileDetailView from './views/FileDetailView'
import ConflictsView from './views/ConflictsView'
import LiveView from './views/LiveView'
import { useBelayStore, isWatcherHealthy, isWatcherDegraded, getWatcherSeverity, formatTimeSince } from './stores/belayStore'
import { connectStream } from './services/api'
import type { BelayEvent } from './types'

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

const NAV_ITEMS = [
  { to: '/timeline', icon: List, label: 'TIMELINE' },
  { to: '/sessions', icon: Activity, label: 'SESSIONS' },
  { to: '/files', icon: HardDrive, label: 'FILES' },
  { to: '/conflicts', icon: WarningTriangle, label: 'CONFLICTS' },
  { to: '/live', icon: GitFork, label: 'LIVE' },
]

function CyberSidebar() {
  const stats = useBelayStore((s) => s.stats)
  const connected = useBelayStore((s) => s.connected)
  const health = useBelayStore((s) => s.health)
  const location = useLocation()

  const connectionColor = connected ? '#00f0ff' : '#ff00e5'
  const watcherHealthy = isWatcherHealthy(health)
  const watcherDegraded = isWatcherDegraded(health)
  const watcherColor = watcherHealthy ? '#00ff88' : watcherDegraded ? '#ffaa00' : '#ff3366'

  return (
    <aside
      className="hidden md:flex flex-col w-56 lg:w-64 shrink-0 border-r overflow-y-auto"
      style={{
        background: 'rgba(10, 14, 23, 0.95)',
        borderColor: 'rgba(0, 240, 255, 0.1)',
      }}
    >
      <div className="p-4 pb-3">
        <div className="flex items-center gap-2.5 mb-1">
          <Clock className="w-5 h-5" style={{ color: '#00f0ff' }} />
          <span
            className="text-lg font-black font-mono tracking-tight"
            style={{
              color: '#ffffff',
              textShadow: '0 0 10px rgba(0, 240, 255, 0.4)',
            }}
          >
            BELAY
          </span>
        </div>
        <div
          className="font-mono text-xs tracking-[0.25em] ml-0.5"
          style={{ color: 'rgba(0, 240, 255, 0.7)' }}
        >
          FILESYSTEM JOURNAL
        </div>
      </div>

      <div
        className="mx-3 mb-3 px-3 py-1.5 flex items-center gap-2 font-mono text-xs"
        style={{
          border: `1px solid ${connectionColor}25`,
          borderRadius: '2px',
          background: `${connectionColor}08`,
        }}
      >
        <span
          className="w-2 h-2 rounded-full shrink-0"
          style={{
            backgroundColor: connectionColor,
            boxShadow: `0 0 6px ${connectionColor}, 0 0 12px ${connectionColor}60`,
            animation: connected ? 'cyber-pulse 2s ease-in-out infinite' : 'none',
          }}
        />
        <span
          className="tracking-widest font-bold"
          style={{ color: connectionColor, textShadow: `0 0 4px ${connectionColor}60` }}
        >
          {connected ? 'LINK ACTIVE' : 'LINK DOWN'}
        </span>
        {connected ? (
          <Flash className="w-3 h-3 ml-auto" style={{ color: connectionColor }} />
        ) : (
          <FlashOff className="w-3 h-3 ml-auto" style={{ color: connectionColor }} />
        )}
      </div>

      {health?.watcher && (
        <div
          className="mx-3 mb-3 px-3 py-1.5 flex items-center gap-2 font-mono text-xs"
          style={{
            border: `1px solid ${watcherColor}25`,
            borderRadius: '2px',
            background: `${watcherColor}08`,
          }}
        >
          <span
            className="w-2 h-2 rounded-full shrink-0"
            style={{
              backgroundColor: watcherColor,
              boxShadow: `0 0 6px ${watcherColor}, 0 0 12px ${watcherColor}60`,
              animation: watcherHealthy ? 'cyber-pulse 2s ease-in-out infinite' : 'none',
            }}
          />
          <span
            className="tracking-widest font-bold"
            style={{ color: watcherColor, textShadow: `0 0 4px ${watcherColor}60` }}
          >
            {watcherHealthy ? 'WATCHER ACTIVE' : watcherDegraded ? 'WATCHER DEGRADED' : 'WATCHER DOWN'}
          </span>
          {watcherHealthy ? (
            <Eye className="w-3 h-3 ml-auto" style={{ color: watcherColor }} />
          ) : (
            <EyeClosed className="w-3 h-3 ml-auto" style={{ color: watcherColor }} />
          )}
        </div>
      )}

      <div className="cyber-divider mx-3" />

      <nav className="flex-1 py-3 px-2 space-y-0.5">
        {NAV_ITEMS.map((item) => {
          const isActive = location.pathname.startsWith(item.to)
          const Icon = item.icon
          return (
            <NavLink
              key={item.to}
              to={item.to}
              className="flex items-center gap-3 px-3 py-2 rounded-sm font-mono text-xs tracking-wider transition-all"
              style={{
                color: isActive ? '#00f0ff' : '#ffffff',
                background: isActive ? 'rgba(0, 240, 255, 0.08)' : 'transparent',
                borderLeft: isActive ? '2px solid #00f0ff' : '2px solid transparent',
                textShadow: isActive ? '0 0 8px rgba(0, 240, 255, 0.4)' : 'none',
              }}
            >
              <Icon className="w-4 h-4" />
              {item.label}
            </NavLink>
          )
        })}
      </nav>

      {stats && (
        <div
          className="p-3 mx-3 mb-3 font-mono text-xs space-y-1.5"
          style={{
            borderTop: '1px solid rgba(0, 240, 255, 0.08)',
          }}
        >
          <div className="flex justify-between">
            <span style={{ color: '#ffffff' }}>EVENTS</span>
            <span style={{ color: '#00f0ff' }}>{stats.total_events.toLocaleString()}</span>
          </div>
          <div className="flex justify-between">
            <span style={{ color: '#ffffff' }}>SESSIONS</span>
            <span style={{ color: '#ff00e5' }}>{stats.total_sessions}</span>
          </div>
          <div className="flex justify-between">
            <span style={{ color: '#ffffff' }}>ACTIVE</span>
            <span style={{ color: '#00ff88' }}>{stats.active_sessions}</span>
          </div>
          <div className="flex justify-between">
            <span style={{ color: '#ffffff' }}>OBJECTS</span>
            <span style={{ color: '#ffaa00' }}>{stats.store_objects.toLocaleString()}</span>
          </div>
          <div className="flex justify-between">
            <span style={{ color: '#ffffff' }}>DATA SIZE</span>
            <span style={{ color: '#b388ff' }}>{formatBytes(stats.store_bytes)}</span>
          </div>
        </div>
      )}
    </aside>
  )
}

function CyberMobileNav() {
  const location = useLocation()

  return (
    <nav
      className="md:hidden fixed bottom-0 left-0 right-0 z-40 safe-area-bottom flex items-center justify-around px-1 py-2"
      style={{
        background: 'rgba(10, 14, 23, 0.95)',
        backdropFilter: 'blur(12px)',
        borderTop: '1px solid rgba(0, 240, 255, 0.15)',
      }}
    >
      {NAV_ITEMS.map((item) => {
        const isActive = location.pathname.startsWith(item.to)
        const Icon = item.icon
        return (
          <NavLink
            key={item.to}
            to={item.to}
            className="flex flex-col items-center gap-0.5 px-2 py-1"
          >
            <Icon
              className="w-5 h-5"
              style={{
                color: isActive ? '#00f0ff' : '#ffffff',
                filter: isActive ? 'drop-shadow(0 0 4px rgba(0, 240, 255, 0.5))' : 'none',
              }}
            />
            <span
              className="font-mono text-xs tracking-wider"
              style={{
                color: isActive ? '#00f0ff' : '#ffffff',
              }}
            >
              {item.label}
            </span>
          </NavLink>
        )
      })}
    </nav>
  )
}

function sseToEvent(data: Record<string, unknown>): BelayEvent {
  return {
    event_id: String(data.event_id || ''),
    file_path: String(data.file_path || ''),
    operation: String(data.op || 'MODIFY').toUpperCase(),
    timestamp_nano: typeof data.timestamp === 'number' ? data.timestamp : Date.now() * 1e6,
    session_id: String(data.session_id || ''),
    content_hash: String(data.content_hash || ''),
    previous_hash: '',
    content_size: typeof data.size === 'number' ? data.size : 0,
    attribution_method: 'hook',
    attribution_confidence: 0,
    attribution: String(data.attribution || ''),
    metadata: {},
  }
}

function HealthBanner() {
  const health = useBelayStore((s) => s.health)

  if (!health?.watcher || health.watcher.status === 'running') return null

  const severity = getWatcherSeverity(health)
  const isCritical = severity === 'critical'
  const isDegraded = health.watcher.status === 'degraded'
  const bannerColor = isCritical ? '#ff3366' : '#ffaa00'

  let title: string
  let description: string
  if (isDegraded) {
    title = 'FILE WATCHER DEGRADED'
    description = health.watcher.error || 'Watcher is running but has not seen events recently.'
  } else if (health.watcher.status === 'error') {
    title = 'FILE WATCHER ERROR'
    description = health.watcher.error || 'Belay is not capturing file changes.'
  } else {
    title = 'FILE WATCHER OFFLINE'
    description = 'Belay is not capturing file changes.'
  }

  return (
    <div
      className="px-4 py-3 font-mono text-sm flex flex-col gap-1"
      style={{
        background: `${bannerColor}12`,
        borderBottom: `2px solid ${bannerColor}`,
        color: '#ffffff',
      }}
    >
      <div className="flex items-center gap-2 flex-wrap">
        <WarningTriangle className="w-4 h-4 shrink-0" style={{ color: bannerColor }} />
        <span className="font-bold tracking-wider" style={{ color: bannerColor }}>
          {title}
        </span>
        <span className="tracking-wide" style={{ color: '#E4E4E7' }}>
          {description}{health.watcher.last_event_at ? ` Last event: ${formatTimeSince(health.watcher.last_event_at)}` : ''}
        </span>
      </div>
      <div className="text-xs tracking-wider" style={{ color: '#A1A1AA' }}>
        Run: <span style={{ color: bannerColor }}>thelab restart belay-api</span>
      </div>
    </div>
  )
}

export default function App() {
  const fetchStats = useBelayStore((s) => s.fetchStats)
  const fetchHealth = useBelayStore((s) => s.fetchHealth)
  const setConnected = useBelayStore((s) => s.setConnected)
  const addLiveEvent = useBelayStore((s) => s.addLiveEvent)
  useEffect(() => {
    fetchStats()
    fetchHealth()
    const interval = setInterval(() => {
      fetchStats()
      fetchHealth()
    }, 10000)
    return () => clearInterval(interval)
  }, [fetchStats, fetchHealth])

  useEffect(() => {
    const es = connectStream((msg) => {
      if (msg.type === 'connected') {
        setConnected(true)
      } else if (msg.type === 'file_event') {
        const data = msg.data as Record<string, unknown>
        if (typeof msg.timestamp === 'number') {
          data.timestamp = msg.timestamp
        }
        addLiveEvent(sseToEvent(data))
      }
    })
    es.onerror = () => setConnected(false)
    return () => {
      es.close()
      setConnected(false)
    }
  }, [setConnected, addLiveEvent])

  return (
    <div className="h-screen flex overflow-hidden safe-area-top" style={{ background: '#0a0e17' }}>
      <div className="cyber-hex-bg fixed inset-0 pointer-events-none" />


      <CyberSidebar />

      <main className="flex-1 overflow-y-auto scrollbar-cyber relative z-10 pb-20 md:pb-0">
        <HealthBanner />
        <Routes>
          <Route path="/" element={<Navigate to="/timeline" replace />} />
          <Route path="/timeline" element={<TimelineView />} />
          <Route path="/sessions" element={<SessionsView />} />
          <Route path="/sessions/:id" element={<SessionDetailView />} />
          <Route path="/files" element={<FilesView />} />
          <Route path="/files/*" element={<FileDetailView />} />
          <Route path="/conflicts" element={<ConflictsView />} />
          <Route path="/live" element={<LiveView />} />
        </Routes>
      </main>

      <CyberMobileNav />
    </div>
  )
}
