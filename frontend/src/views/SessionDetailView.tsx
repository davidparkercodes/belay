import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { NavArrowLeft, Activity, Clock, Terminal, HardDrive } from 'iconoir-react'
import type { BelaySession, BelayEvent } from '../types'
import * as api from '../services/api'
import EventRow from '../components/EventRow'
import CyberPageHeader from '../components/CyberPageHeader'
import CyberPanel, { CyberPanelHeader } from '../components/CyberPanel'
import CyberStatCard from '../components/CyberStatCard'

export default function SessionDetailView() {
  const { id } = useParams<{ id: string }>()
  const [session, setSession] = useState<BelaySession | null>(null)
  const [events, setEvents] = useState<BelayEvent[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!id) return
    setLoading(true)
    Promise.all([
      api.getSession(id).catch(() => null),
      api.getSessionEvents(id, 100).catch(() => ({ events: [] })),
    ]).then(([sess, evts]) => {
      setSession(sess)
      setEvents(evts.events)
    }).finally(() => setLoading(false))
  }, [id])

  if (loading) {
    return (
      <div className="flex justify-center py-16 font-mono">
        <div
          className="text-sm tracking-widest animate-pulse"
          style={{
            color: '#00f0ff',
            textShadow: '0 0 12px rgba(0, 240, 255, 0.6), 0 0 30px rgba(0, 240, 255, 0.3)',
          }}
        >
          LOADING SESSION DATA...
        </div>
      </div>
    )
  }

  if (!session) {
    return (
      <div className="p-4 md:p-6 font-mono">
        <Link
          to="/sessions"
          className="inline-flex items-center gap-2 px-3 py-1.5 mb-6 font-mono text-[11px] tracking-wider transition-all"
          style={{
            color: '#00f0ff',
            border: '1px solid rgba(0, 240, 255, 0.25)',
            borderRadius: '2px',
            background: 'rgba(0, 240, 255, 0.05)',
            textShadow: '0 0 8px rgba(0, 240, 255, 0.4)',
          }}
        >
          <NavArrowLeft className="w-3.5 h-3.5" />
          [ BACK TO SESSIONS ]
        </Link>
        <div
          className="text-sm tracking-wider"
          style={{ color: '#ff3366', textShadow: '0 0 8px rgba(255, 51, 102, 0.4)' }}
        >
          SESSION NOT FOUND
        </div>
      </div>
    )
  }

  const isActive = session.status === 'active'
  const statusColor = isActive ? '#00ff88' : '#ffffff'
  const statusLabel = isActive ? 'LIVE' : 'ENDED'

  return (
    <div className="p-4 md:p-6 max-w-5xl font-mono">
      <Link
        to="/sessions"
        className="inline-flex items-center gap-2 px-3 py-1.5 mb-5 font-mono text-[11px] tracking-wider transition-all"
        style={{
          color: '#00f0ff',
          border: '1px solid rgba(0, 240, 255, 0.25)',
          borderRadius: '2px',
          background: 'rgba(0, 240, 255, 0.05)',
          textShadow: '0 0 8px rgba(0, 240, 255, 0.4)',
        }}
      >
        <NavArrowLeft className="w-3.5 h-3.5" />
        [ BACK TO SESSIONS ]
      </Link>

      <CyberPageHeader
        icon={
          <span
            className="relative flex items-center"
          >
            <span
              className="w-2.5 h-2.5 rounded-full"
              style={{
                backgroundColor: statusColor,
                boxShadow: isActive
                  ? `0 0 8px ${statusColor}, 0 0 16px ${statusColor}60`
                  : 'none',
                animation: isActive ? 'cyber-pulse 2s ease-in-out infinite' : 'none',
              }}
            />
          </span>
        }
        title={session.session_id.slice(0, 16)}
        subtitle={session.label || `${session.tool_name} SESSION`}
        color={isActive ? '#00ff88' : '#00f0ff'}
      />

      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-6">
        <CyberStatCard
          icon={<Activity className="w-4 h-4" />}
          label="STATUS"
          value={statusLabel}
          unit={session.status}
          color={isActive ? '#00ff88' : '#ff00e5'}
        />
        <CyberStatCard
          icon={<Clock className="w-4 h-4" />}
          label="DURATION"
          value={session.duration || '---'}
          unit="elapsed"
          color="#00f0ff"
        />
        <CyberStatCard
          icon={<Activity className="w-4 h-4" />}
          label="EVENTS"
          value={String(session.event_count)}
          unit="recorded"
          color="#ff00e5"
        />
        <CyberStatCard
          icon={<HardDrive className="w-4 h-4" />}
          label="FILES"
          value={String(session.files_changed)}
          unit="modified"
          color="#ffaa00"
        />
      </div>

      <CyberPanel className="mb-6">
        <CyberPanelHeader
          icon={<Terminal className="w-4 h-4" />}
          title="SESSION DETAILS"
          color="#ff00e5"
        />
        <div className="space-y-2.5 text-sm">
          <DetailRow label="TOOL" value={session.tool_name} />
          <DetailRow label="PID" value={session.pid > 0 ? String(session.pid) : '\u2014'} mono />
          <DetailRow label="WORKING DIR" value={session.working_directory} mono small />
          <DetailRow label="STARTED" value={new Date(session.started_at).toLocaleString()} />
          {session.ended_at && (
            <DetailRow label="ENDED" value={new Date(session.ended_at).toLocaleString()} />
          )}
          <DetailRow label="SESSION ID" value={session.session_id} mono small />
        </div>
      </CyberPanel>

      <CyberPanel>
        <CyberPanelHeader
          icon={<Activity className="w-4 h-4" />}
          title="SESSION EVENTS"
          color="#00f0ff"
          right={`${events.length} RECORDS`}
        />
        {events.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-12">
            <Activity
              className="w-8 h-8 mb-3"
              style={{ color: 'rgba(0, 240, 255, 0.4)' }}
            />
            <div
              className="font-mono text-sm tracking-widest"
              style={{ color: '#ffffff' }}
            >
              NO EVENTS RECORDED
            </div>
          </div>
        ) : (
          <div className="space-y-0 scrollbar-cyber overflow-y-auto pr-1" style={{ maxHeight: '60vh' }}>
            {events.map((event) => (
              <EventRow key={event.event_id} event={event} showSession={false} />
            ))}
          </div>
        )}
      </CyberPanel>
    </div>
  )
}

function DetailRow({ label, value, mono, small }: { label: string; value: string; mono?: boolean; small?: boolean }) {
  return (
    <div className="flex items-baseline gap-3">
      <span
        className="font-mono text-xs tracking-widest font-bold shrink-0 w-28"
        style={{ color: 'rgba(255, 0, 229, 0.8)' }}
      >
        {label}
      </span>
      <span
        className={`${mono ? 'font-mono' : ''} ${small ? 'text-xs' : 'text-sm'} truncate`}
        style={{ color: '#ffffff' }}
        title={value}
      >
        {value}
      </span>
    </div>
  )
}
