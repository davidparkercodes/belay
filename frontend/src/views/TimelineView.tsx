import { useEffect, useState } from 'react'
import { List, RefreshDouble, Activity, WarningTriangle } from 'iconoir-react'
import { useBelayStore, isWatcherHealthy, isWatcherDegraded, formatTimeSince } from '../stores/belayStore'
import CyberPageHeader from '../components/CyberPageHeader'
import CyberPanel, { CyberPanelHeader } from '../components/CyberPanel'
import EventRow from '../components/EventRow'

const TIME_RANGES = [
  { label: '1H', value: '1h' },
  { label: '6H', value: '6h' },
  { label: '24H', value: '24h' },
  { label: '7D', value: '168h' },
]

const ATTRIBUTION_FILTERS = [
  { label: 'ALL', value: '', color: '#ffffff' },
  { label: 'AI', value: 'ai', color: '#00C8FF' },
  { label: 'HUMAN', value: 'none', color: '#00FF82' },
  { label: 'GIT', value: 'git', color: '#FFAA00' },
]

export default function TimelineView() {
  const [range, setRange] = useState('24h')
  const [attrFilter, setAttrFilter] = useState('')
  const events = useBelayStore((s) => s.events)
  const loading = useBelayStore((s) => s.loadingEvents)
  const eventsError = useBelayStore((s) => s.eventsError)
  const fetchEvents = useBelayStore((s) => s.fetchEvents)
  const health = useBelayStore((s) => s.health)
  const watcherDown = !isWatcherHealthy(health)
  const degraded = isWatcherDegraded(health)

  useEffect(() => {
    fetchEvents({ since: range, limit: 200, attribution: attrFilter || undefined })
  }, [fetchEvents, range, attrFilter])

  return (
    <div className="p-4 md:p-6 max-w-5xl font-mono">
      <CyberPageHeader
        icon={<List className="w-6 h-6" />}
        title="TIMELINE"
        subtitle="ALL FILE CHANGE EVENTS ACROSS SESSIONS"
        color="#00f0ff"
        right={
          <button
            onClick={() => fetchEvents({ since: range, limit: 200, attribution: attrFilter || undefined })}
            className="flex items-center gap-2 px-3 py-1.5 font-mono text-xs tracking-wider transition-all"
            style={{
              color: '#00f0ff',
              border: '1px solid rgba(0, 240, 255, 0.3)',
              borderRadius: '2px',
              background: 'rgba(0, 240, 255, 0.05)',
            }}
          >
            <RefreshDouble className="w-3.5 h-3.5" />
            REFRESH
          </button>
        }
      />

      <div className="flex items-center gap-2 mb-3 flex-wrap">
        {TIME_RANGES.map((tr) => {
          const isActive = range === tr.value
          return (
            <button
              key={tr.value}
              onClick={() => setRange(tr.value)}
              className="px-3 py-1.5 font-mono text-xs tracking-wider transition-all"
              style={{
                color: isActive ? '#0a0e17' : '#ffffff',
                background: isActive
                  ? '#00f0ff'
                  : 'rgba(0, 240, 255, 0.06)',
                border: isActive
                  ? '1px solid #00f0ff'
                  : '1px solid rgba(0, 240, 255, 0.15)',
                borderRadius: '2px',
                boxShadow: isActive
                  ? '0 0 12px rgba(0, 240, 255, 0.4), 0 0 24px rgba(0, 240, 255, 0.15)'
                  : 'none',
                fontWeight: isActive ? 700 : 500,
                textShadow: isActive ? 'none' : '0 0 4px rgba(0, 240, 255, 0.2)',
              }}
            >
              {tr.label}
            </button>
          )
        })}
      </div>

      <div className="flex items-center gap-2 mb-5 flex-wrap">
        {ATTRIBUTION_FILTERS.map((af) => {
          const isActive = attrFilter === af.value
          return (
            <button
              key={af.value}
              onClick={() => setAttrFilter(af.value)}
              className="px-3 py-1.5 font-mono text-xs tracking-wider transition-all"
              style={{
                color: isActive ? '#0a0e17' : af.color,
                background: isActive
                  ? af.color
                  : `${af.color}10`,
                border: isActive
                  ? `1px solid ${af.color}`
                  : `1px solid ${af.color}28`,
                borderRadius: '2px',
                boxShadow: isActive
                  ? `0 0 12px ${af.color}66, 0 0 24px ${af.color}28`
                  : 'none',
                fontWeight: isActive ? 700 : 500,
                textShadow: isActive ? 'none' : `0 0 4px ${af.color}40`,
              }}
            >
              {af.label}
            </button>
          )
        })}
      </div>

      {eventsError && (
        <div
          className="mb-4 px-4 py-3 text-sm tracking-wide font-mono"
          style={{
            color: '#ff3366',
            background: 'rgba(255, 51, 102, 0.08)',
            border: '1px solid rgba(255, 51, 102, 0.25)',
            borderRadius: '2px',
          }}
        >
          Failed to load events: {eventsError}
        </div>
      )}

      {loading ? (
        <div className="flex justify-center py-16">
          <div
            className="font-mono text-sm tracking-widest animate-pulse"
            style={{
              color: '#00f0ff',
              textShadow: '0 0 12px rgba(0, 240, 255, 0.6), 0 0 30px rgba(0, 240, 255, 0.3)',
            }}
          >
            LOADING DATA...
          </div>
        </div>
      ) : !events || events.length === 0 ? (
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
                ? 'Watcher is running but may not be recording changes.'
                : 'Belay is not recording file changes.'}
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
            <Activity
              className="w-10 h-10 mb-4"
              style={{ color: 'rgba(0, 240, 255, 0.5)' }}
            />
            <div
              className="font-mono text-sm tracking-widest mb-2"
              style={{
                color: '#ffffff',
                textShadow: '0 0 8px rgba(0, 240, 255, 0.4)',
              }}
            >
              AWAITING DATA INPUT...
            </div>
            <div
              className="font-mono text-xs tracking-wider"
              style={{ color: 'rgba(0, 240, 255, 0.7)' }}
            >
              File change events will stream here
            </div>
          </div>
        )
      ) : (
        <CyberPanel>
          <CyberPanelHeader
            icon={<Activity className="w-4 h-4" />}
            title="EVENT DATASTREAM"
            color="#00f0ff"
            right={`${events?.length ?? 0} RECORDS`}
          />
          <div className="space-y-0 scrollbar-cyber overflow-y-auto pr-1" style={{ maxHeight: '70vh' }}>
            {events.map((event) => (
              <EventRow key={event.event_id} event={event} />
            ))}
          </div>
        </CyberPanel>
      )}
    </div>
  )
}
