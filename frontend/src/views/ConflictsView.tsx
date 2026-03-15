import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { WarningTriangle } from 'iconoir-react'
import type { BelayConflict } from '../types'
import * as api from '../services/api'
import CyberPageHeader from '../components/CyberPageHeader'

const SEVERITY_CONFIG: Record<number, { label: string; color: string }> = {
  0: { label: 'LOW', color: '#00f0ff' },
  1: { label: 'MEDIUM', color: '#ffaa00' },
  2: { label: 'HIGH', color: '#ff3366' },
  3: { label: 'CRITICAL', color: '#ff00e5' },
}

const TIME_RANGES = [
  { label: '1h', value: '1h' },
  { label: '6h', value: '6h' },
  { label: '24h', value: '24h' },
  { label: '7d', value: '168h' },
]

function formatTime(nanos: number): string {
  if (!nanos) return '-'
  return new Date(nanos / 1e6).toLocaleString()
}

export default function ConflictsView() {
  const [conflicts, setConflicts] = useState<BelayConflict[]>([])
  const [loading, setLoading] = useState(true)
  const [since, setSince] = useState('24h')

  useEffect(() => {
    setLoading(true)
    api.getConflicts({ since })
      .then((data) => setConflicts(data.conflicts || []))
      .finally(() => setLoading(false))
  }, [since])

  const criticalCount = conflicts.filter((c) => c.Severity >= 2).length

  return (
    <div className="p-4 md:p-6 max-w-5xl font-mono">
      <CyberPageHeader
        icon={<WarningTriangle width={28} height={28} />}
        title="CONFLICTS"
        subtitle="OVERLAPPING MODIFICATIONS ACROSS SESSIONS"
        color="#ffaa00"
      />

      {/* Time range buttons + count */}
      <div className="flex items-center gap-2 mb-6 flex-wrap">
        {TIME_RANGES.map(({ label, value }) => {
          const active = since === value
          return (
            <button
              key={value}
              onClick={() => setSince(value)}
              className="px-4 py-1.5 text-xs font-bold tracking-wider rounded-sm transition-all"
              style={{
                color: active ? '#0a0e17' : '#00f0ff',
                background: active ? '#00f0ff' : 'rgba(0, 240, 255, 0.08)',
                border: `1px solid ${active ? '#00f0ff' : 'rgba(0, 240, 255, 0.25)'}`,
                boxShadow: active ? '0 0 12px rgba(0, 240, 255, 0.4), 0 0 30px rgba(0, 240, 255, 0.15)' : 'none',
                textShadow: active ? 'none' : '0 0 6px rgba(0, 240, 255, 0.5)',
              }}
            >
              {label}
            </button>
          )
        })}

        <span
          className="text-xs ml-3 tracking-wide"
          style={{ color: '#ffffff' }}
        >
          {conflicts.length} conflict{conflicts.length !== 1 ? 's' : ''}
          {criticalCount > 0 && (
            <span
              style={{
                color: '#ff3366',
                textShadow: '0 0 8px rgba(255, 51, 102, 0.6)',
                marginLeft: 8,
              }}
            >
              ({criticalCount} high/critical)
            </span>
          )}
        </span>
      </div>

      {/* Content */}
      {loading ? (
        <div className="flex flex-col items-center justify-center py-20">
          <div
            className="text-sm tracking-widest animate-pulse"
            style={{ color: '#00f0ff', textShadow: '0 0 10px rgba(0, 240, 255, 0.5)' }}
          >
            SCANNING FOR CONFLICTS...
          </div>
        </div>
      ) : conflicts.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20">
          <div
            className="text-sm tracking-widest mb-2"
            style={{ color: '#00ff88', textShadow: '0 0 10px rgba(0, 255, 136, 0.4)' }}
          >
            NO CONFLICTS DETECTED
          </div>
          <div
            className="text-xs tracking-wide"
            style={{ color: '#ffffff' }}
          >
            No overlapping modifications found in this time range
          </div>
        </div>
      ) : (
        <div className="space-y-3">
          {conflicts.map((conflict) => {
            const sev = SEVERITY_CONFIG[conflict.Severity] || SEVERITY_CONFIG[0]
            return (
              <div
                key={conflict.ID}
                className="cyber-panel p-4"
                style={{
                  borderLeft: `2px solid ${sev.color}`,
                  boxShadow: `inset 2px 0 8px ${sev.color}20`,
                }}
              >
                {/* Header: file path + severity badge */}
                <div className="flex items-start justify-between gap-3 mb-2">
                  <Link
                    to={`/files/${encodeURIComponent(conflict.FilePath)}`}
                    className="text-sm font-medium transition-colors truncate hover:text-[#00f0ff] focus:outline focus:outline-1 focus:outline-[#00f0ff]"
                    style={{ color: '#e0e8ff' }}
                  >
                    {conflict.FilePath}
                  </Link>

                  <span
                    className="inline-flex items-center px-2 py-0.5 rounded-sm font-bold text-xs tracking-wider shrink-0"
                    style={{
                      color: sev.color,
                      background: `${sev.color}15`,
                      border: `1px solid ${sev.color}30`,
                      textShadow: `0 0 6px ${sev.color}60`,
                    }}
                  >
                    {sev.label}
                  </span>
                </div>

                {/* Description */}
                {conflict.Description && (
                  <p
                    className="text-xs mb-2 leading-relaxed"
                    style={{ color: '#ffffff' }}
                  >
                    {conflict.Description}
                  </p>
                )}

                {/* Time range + session count */}
                <div
                  className="flex items-center gap-4 text-[11px] mb-2"
                  style={{ color: '#ffffff' }}
                >
                  <span>
                    {conflict.Sessions?.length || 0} session{(conflict.Sessions?.length || 0) !== 1 ? 's' : ''} involved
                  </span>
                  <span className="tabular-nums">
                    {formatTime(conflict.FirstTime)} &mdash; {formatTime(conflict.LastTime)}
                  </span>
                </div>

                {/* Session links */}
                {conflict.Sessions && conflict.Sessions.length > 0 && (
                  <div className="flex gap-2 flex-wrap">
                    {conflict.Sessions.map((sid) => (
                      <Link
                        key={sid}
                        to={`/sessions/${sid}`}
                        className="text-xs px-2 py-0.5 rounded-sm tracking-wider transition-all hover:bg-[rgba(0,240,255,0.15)] hover:border-[rgba(0,240,255,0.4)] hover:[text-shadow:0_0_6px_rgba(0,240,255,0.5)] focus:outline focus:outline-1 focus:outline-[#00f0ff]"
                        style={{
                          color: '#00f0ff',
                          background: 'rgba(0, 240, 255, 0.08)',
                          border: '1px solid rgba(0, 240, 255, 0.2)',
                        }}
                      >
                        {sid.slice(0, 8)}
                      </Link>
                    ))}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
