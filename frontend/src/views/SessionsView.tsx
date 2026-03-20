import { useEffect, useState } from 'react'
import { Terminal, Activity, WarningTriangle } from 'iconoir-react'
import { useBelayStore, isWatcherHealthy, isWatcherDegraded, formatTimeSince } from '../stores/belayStore'
import CyberPageHeader from '../components/CyberPageHeader'
import SessionCard from '../components/SessionCard'

export default function SessionsView() {
  const [showActiveOnly, setShowActiveOnly] = useState(false)
  const sessions = useBelayStore((s) => s.sessions)
  const loading = useBelayStore((s) => s.loadingSessions)
  const sessionsError = useBelayStore((s) => s.sessionsError)
  const fetchSessions = useBelayStore((s) => s.fetchSessions)
  const health = useBelayStore((s) => s.health)
  const watcherDown = !isWatcherHealthy(health)
  const degraded = isWatcherDegraded(health)

  useEffect(() => {
    fetchSessions(showActiveOnly)
  }, [fetchSessions, showActiveOnly])

  const activeSessions = sessions.filter((s) => s.status === 'active')
  const endedSessions = sessions.filter((s) => s.status !== 'active')

  return (
    <div className="p-4 md:p-6 max-w-5xl font-mono">
      <CyberPageHeader
        icon={<Terminal className="w-6 h-6" />}
        title="SESSIONS"
        subtitle="AI DEVELOPMENT SESSIONS DETECTED BY BELAY"
        color="#ff00e5"
      />

      {/* Filter buttons */}
      <div className="flex items-center gap-3 mb-6">
        <button
          onClick={() => setShowActiveOnly(false)}
          className="px-3 py-1.5 font-mono text-xs tracking-wider transition-all"
          style={{
            color: !showActiveOnly ? '#0a0e17' : '#ffffff',
            background: !showActiveOnly
              ? '#ff00e5'
              : 'rgba(255, 0, 229, 0.06)',
            border: !showActiveOnly
              ? '1px solid #ff00e5'
              : '1px solid rgba(255, 0, 229, 0.15)',
            borderRadius: '2px',
            boxShadow: !showActiveOnly
              ? '0 0 12px rgba(255, 0, 229, 0.4), 0 0 24px rgba(255, 0, 229, 0.15)'
              : 'none',
            fontWeight: !showActiveOnly ? 700 : 500,
          }}
        >
          ALL ({sessions.length})
        </button>
        <button
          onClick={() => setShowActiveOnly(true)}
          className="px-3 py-1.5 font-mono text-xs tracking-wider transition-all"
          style={{
            color: showActiveOnly ? '#0a0e17' : '#ffffff',
            background: showActiveOnly
              ? '#00ff88'
              : 'rgba(0, 255, 136, 0.06)',
            border: showActiveOnly
              ? '1px solid #00ff88'
              : '1px solid rgba(0, 255, 136, 0.15)',
            borderRadius: '2px',
            boxShadow: showActiveOnly
              ? '0 0 12px rgba(0, 255, 136, 0.4), 0 0 24px rgba(0, 255, 136, 0.15)'
              : 'none',
            fontWeight: showActiveOnly ? 700 : 500,
          }}
        >
          ACTIVE ({activeSessions.length})
        </button>
      </div>

      {sessionsError && (
        <div
          className="mb-4 px-4 py-3 text-sm tracking-wide"
          style={{
            color: '#ff3366',
            background: 'rgba(255, 51, 102, 0.08)',
            border: '1px solid rgba(255, 51, 102, 0.25)',
            borderRadius: '2px',
          }}
        >
          Failed to load sessions: {sessionsError}
        </div>
      )}

      {loading ? (
        <div className="flex justify-center py-16">
          <div
            className="font-mono text-sm tracking-widest animate-pulse"
            style={{
              color: '#ff00e5',
              textShadow: '0 0 12px rgba(255, 0, 229, 0.6), 0 0 30px rgba(255, 0, 229, 0.3)',
            }}
          >
            SCANNING SESSIONS...
          </div>
        </div>
      ) : sessions.length === 0 ? (
        watcherDown ? (
          <div className="flex flex-col items-center justify-center py-16">
            <WarningTriangle
              className="w-12 h-12 mb-4"
              style={{
                color: degraded ? '#ffaa00' : '#ff3366',
                filter: `drop-shadow(0 0 8px ${degraded ? 'rgba(255, 170, 0, 0.5)' : 'rgba(255, 51, 102, 0.5)'})`,
              }}
            />
            <div
              className="font-mono text-base font-bold tracking-widest mb-2"
              style={{
                color: degraded ? '#ffaa00' : '#ff3366',
                textShadow: `0 0 10px ${degraded ? 'rgba(255, 170, 0, 0.5)' : 'rgba(255, 51, 102, 0.5)'}`,
              }}
            >
              {degraded ? 'FILE WATCHER DEGRADED' : 'FILE WATCHER IS DOWN'}
            </div>
            <div
              className="font-mono text-sm tracking-wider mb-1"
              style={{ color: '#E4E4E7' }}
            >
              {degraded
                ? 'Watcher may not be detecting new sessions.'
                : 'New sessions will not be detected.'}
              {health?.watcher?.last_event_at && (
                <> Last event: {formatTimeSince(health.watcher.last_event_at)}</>
              )}
            </div>
            {health?.watcher?.error && (
              <div
                className="font-mono text-sm tracking-wider mb-1"
                style={{ color: degraded ? '#ffaa00' : '#ff3366' }}
              >
                {health.watcher.error}
              </div>
            )}
            <div
              className="font-mono text-xs tracking-wider mt-3 px-4 py-2"
              style={{
                color: '#A1A1AA',
                background: `${degraded ? 'rgba(255, 170, 0, 0.06)' : 'rgba(255, 51, 102, 0.06)'}`,
                border: `1px solid ${degraded ? 'rgba(255, 170, 0, 0.2)' : 'rgba(255, 51, 102, 0.2)'}`,
                borderRadius: '2px',
              }}
            >
              Run: <span style={{ color: degraded ? '#ffaa00' : '#ff3366' }}>belay daemon restart</span>
            </div>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center py-16">
            <Terminal
              className="w-10 h-10 mb-4"
              style={{ color: 'rgba(255, 0, 229, 0.5)' }}
            />
            <div
              className="font-mono text-sm tracking-widest mb-2"
              style={{
                color: '#ffffff',
                textShadow: '0 0 8px rgba(255, 0, 229, 0.4)',
              }}
            >
              NO SESSIONS DETECTED
            </div>
            <div
              className="font-mono text-xs tracking-wider"
              style={{ color: '#ffffff' }}
            >
              Sessions are detected automatically when AI tools modify files
            </div>
          </div>
        )
      ) : (
        <div className="space-y-6">
          {watcherDown && (
            <div
              className="px-4 py-3 font-mono text-sm flex items-center gap-2 flex-wrap"
              style={{
                background: `${degraded ? '#ffaa00' : '#ff3366'}12`,
                border: `1px solid ${degraded ? 'rgba(255, 170, 0, 0.3)' : 'rgba(255, 51, 102, 0.3)'}`,
                borderRadius: '2px',
                color: '#E4E4E7',
              }}
            >
              <WarningTriangle
                className="w-4 h-4 shrink-0"
                style={{ color: degraded ? '#ffaa00' : '#ff3366' }}
              />
              <span
                className="font-bold tracking-wider"
                style={{ color: degraded ? '#ffaa00' : '#ff3366' }}
              >
                {degraded ? 'WATCHER DEGRADED' : 'WATCHER DOWN'}
              </span>
              <span className="tracking-wide" style={{ color: '#E4E4E7' }}>
                {degraded
                  ? 'New sessions may not be detected.'
                  : 'New sessions will not be detected until the watcher is restarted.'}
              </span>
            </div>
          )}

          {!showActiveOnly && activeSessions.length > 0 && (
            <div>
              <div className="flex items-center gap-2 mb-3">
                <span
                  className="w-2 h-2 rounded-full"
                  style={{
                    backgroundColor: '#00ff88',
                    boxShadow: '0 0 6px #00ff88, 0 0 12px rgba(0, 255, 136, 0.4)',
                    animation: 'cyber-pulse 2s ease-in-out infinite',
                  }}
                />
                <h2
                  className="font-mono text-xs font-bold tracking-widest"
                  style={{
                    color: '#00ff88',
                    textShadow: '0 0 6px rgba(0, 255, 136, 0.5)',
                  }}
                >
                  ACTIVE ({activeSessions.length})
                </h2>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                {activeSessions.map((s) => (
                  <SessionCard key={s.session_id} session={s} />
                ))}
              </div>
            </div>
          )}

          {/* Ended sessions */}
          {!showActiveOnly && endedSessions.length > 0 && (
            <div>
              <div className="flex items-center gap-2 mb-3">
                <span
                  className="w-2 h-2 rounded-full"
                  style={{ backgroundColor: 'rgba(255, 255, 255, 0.6)' }}
                />
                <h2
                  className="font-mono text-xs font-bold tracking-widest"
                  style={{ color: '#ffffff' }}
                >
                  ENDED ({endedSessions.length})
                </h2>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                {endedSessions.map((s) => (
                  <SessionCard key={s.session_id} session={s} />
                ))}
              </div>
            </div>
          )}

          {showActiveOnly && (
            <div className="grid gap-3 sm:grid-cols-2">
              {sessions.filter((s) => s.status === 'active').map((s) => (
                <SessionCard key={s.session_id} session={s} />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
