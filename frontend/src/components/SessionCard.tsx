import type { BelaySession } from '../types'
import { Link } from 'react-router-dom'
import { Activity, Clock, Folder, DesignPencil } from 'iconoir-react'

function shortPath(filePath: string): string {
  const parts = filePath.split('/')
  if (parts.length <= 3) return filePath
  return '.../' + parts.slice(-3).join('/')
}

interface Props {
  session: BelaySession
}

export default function SessionCard({ session }: Props) {
  const isActive = session.status === 'active'
  const statusColor = isActive ? '#00ff88' : '#ffffff'
  const statusLabel = isActive ? 'LIVE' : 'ENDED'

  return (
    <Link to={`/sessions/${session.session_id}`}>
      <div className="cyber-session-card p-3 cursor-pointer">
        {/* Status + tool */}
        <div className="flex items-center justify-between mb-2">
          <div className="flex items-center gap-2">
            <span
              className="w-2 h-2 rounded-full"
              style={{
                backgroundColor: statusColor,
                boxShadow: isActive ? `0 0 6px ${statusColor}, 0 0 12px ${statusColor}60` : 'none',
                animation: isActive ? 'cyber-pulse 2s ease-in-out infinite' : 'none',
              }}
            />
            <span
              className="font-mono text-xs font-bold tracking-widest"
              style={{
                color: statusColor,
                textShadow: isActive ? `0 0 4px ${statusColor}80` : 'none',
              }}
            >
              {statusLabel}
            </span>
          </div>
          <span
            className="font-mono text-xs px-2 py-0.5 rounded-sm tracking-wider"
            style={{
              color: '#00f0ff',
              background: 'rgba(0, 240, 255, 0.1)',
              border: '1px solid rgba(0, 240, 255, 0.2)',
            }}
          >
            {session.tool_name || 'UNKNOWN'}
          </span>
        </div>

        {/* Session ID / label */}
        <div
          className="font-mono text-xs font-medium mb-1.5 truncate"
          style={{ color: '#e0e8ff' }}
          title={session.session_id}
        >
          {session.label || session.session_id.slice(0, 12)}
        </div>

        {/* Working dir */}
        <div
          className="font-mono text-xs mb-2 truncate flex items-center gap-1"
          style={{ color: '#ffffff' }}
          title={session.working_directory}
        >
          <Folder className="w-3 h-3 shrink-0" />
          {session.working_directory ? shortPath(session.working_directory) : '---'}
        </div>

        {/* Stats row */}
        <div
          className="flex items-center gap-4 font-mono text-xs pt-2"
          style={{ borderTop: '1px solid rgba(0, 240, 255, 0.08)' }}
        >
          <div className="flex items-center gap-1">
            <DesignPencil className="w-3 h-3" style={{ color: '#00f0ff' }} />
            <span style={{ color: '#00f0ff' }}>{session.files_changed}</span>
            <span style={{ color: '#ffffff' }}>files</span>
          </div>
          <div className="flex items-center gap-1">
            <Activity className="w-3 h-3" style={{ color: '#ff00e5' }} />
            <span style={{ color: '#ff00e5' }}>{session.event_count}</span>
            <span style={{ color: '#ffffff' }}>events</span>
          </div>
          <div className="flex items-center gap-1 ml-auto">
            <Clock className="w-3 h-3" style={{ color: '#ffffff' }} />
            <span style={{ color: '#ffffff' }}>
              {session.duration || '---'}
            </span>
          </div>
        </div>
      </div>
    </Link>
  )
}
