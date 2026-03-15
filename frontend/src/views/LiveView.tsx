import { useEffect, useRef } from 'react'
import { GitFork, Flash, FlashOff, Activity, Database, Archive } from 'iconoir-react'
import { useBelayStore } from '../stores/belayStore'
import EventRow from '../components/EventRow'
import CyberPageHeader from '../components/CyberPageHeader'
import CyberPanel from '../components/CyberPanel'

export default function LiveView() {
  const events = useBelayStore((s) => s.events)
  const connected = useBelayStore((s) => s.connected)
  const stats = useBelayStore((s) => s.stats)
  const containerRef = useRef<HTMLDivElement>(null)

  // Auto-scroll to top on new events (newest first)
  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.scrollTop = 0
    }
  }, [events.length])

  const connColor = connected ? '#00ff88' : '#ff3366'

  return (
    <div className="p-4 md:p-6 max-w-5xl h-full flex flex-col font-mono">
      <CyberPageHeader
        icon={<GitFork width={28} height={28} />}
        title="LIVE"
        subtitle="REAL-TIME EVENT STREAM"
      />

      {/* Connection status bar */}
      <div
        className="cyber-panel flex items-center gap-3 px-4 py-2.5 mb-4"
        style={{
          borderColor: `${connColor}30`,
          boxShadow: `inset 0 0 12px ${connColor}08, 0 0 8px ${connColor}10`,
        }}
      >
        {/* Pulse dot */}
        <span
          className={`w-2 h-2 rounded-full shrink-0 ${connected ? 'animate-pulse' : ''}`}
          style={{
            background: connColor,
            boxShadow: `0 0 6px ${connColor}, 0 0 12px ${connColor}80`,
          }}
        />

        {/* Status text */}
        <span
          className="text-xs tracking-wider font-bold"
          style={{ color: connColor, textShadow: `0 0 8px ${connColor}60` }}
        >
          {connected ? (
            <>
              <Flash width={12} height={12} className="inline mr-1.5 -mt-0.5" />
              CONNECTED TO EVENT STREAM
            </>
          ) : (
            <>
              <FlashOff width={12} height={12} className="inline mr-1.5 -mt-0.5" />
              DISCONNECTED &mdash; DAEMON MAY NOT BE RUNNING
            </>
          )}
        </span>

        {/* Active sessions count */}
        {stats && (
          <span
            className="ml-auto text-xs tracking-wide tabular-nums"
            style={{ color: '#ffffff' }}
          >
            {stats.active_sessions} active session{stats.active_sessions !== 1 ? 's' : ''}
          </span>
        )}
      </div>

      {/* Stats summary bar */}
      {stats && stats.active_sessions > 0 && (
        <CyberPanel className="mb-4 !p-3">
          <div className="flex items-center gap-6 text-xs">
            <div className="flex items-center gap-2">
              <Activity width={14} height={14} style={{ color: '#00f0ff' }} />
              <span style={{ color: '#ffffff' }}>Events</span>
              <span
                className="tabular-nums font-bold"
                style={{ color: '#00f0ff', textShadow: '0 0 6px rgba(0, 240, 255, 0.4)' }}
              >
                {stats.total_events.toLocaleString()}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <Database width={14} height={14} style={{ color: '#ff00e5' }} />
              <span style={{ color: '#ffffff' }}>Objects</span>
              <span
                className="tabular-nums font-bold"
                style={{ color: '#ff00e5', textShadow: '0 0 6px rgba(255, 0, 229, 0.4)' }}
              >
                {stats.store_objects.toLocaleString()}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <Archive width={14} height={14} style={{ color: '#ffaa00' }} />
              <span style={{ color: '#ffffff' }}>Store</span>
              <span
                className="tabular-nums font-bold"
                style={{ color: '#ffaa00', textShadow: '0 0 6px rgba(255, 170, 0, 0.4)' }}
              >
                {(stats.store_bytes / 1048576).toFixed(1)}MB
              </span>
            </div>
          </div>
        </CyberPanel>
      )}

      {/* Live event list */}
      <div
        ref={containerRef}
        className="flex-1 overflow-y-auto scrollbar-cyber"
        style={{ minHeight: 0 }}
      >
        {events.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20">
            <div
              className="text-sm tracking-widest mb-2 animate-pulse"
              style={{ color: '#00f0ff', textShadow: '0 0 10px rgba(0, 240, 255, 0.5)' }}
            >
              WAITING FOR EVENTS...
            </div>
            <div
              className="text-xs tracking-wide"
              style={{ color: '#ffffff' }}
            >
              File changes will appear here in real-time
            </div>
          </div>
        ) : (
          <div className="space-y-0.5">
            {events.map((event, i) => (
              <div
                key={event.event_id || crypto.randomUUID()}
                style={i === 0 ? {
                  animation: 'pulse-in 0.4s ease-out',
                } : undefined}
              >
                <EventRow event={event} />
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
