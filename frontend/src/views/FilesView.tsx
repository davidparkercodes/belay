import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { HardDrive, WarningTriangle } from 'iconoir-react'
import type { FileInfo } from '../types'
import * as api from '../services/api'
import { useBelayStore, isWatcherHealthy, isWatcherDegraded, formatTimeSince } from '../stores/belayStore'
import CyberPageHeader from '../components/CyberPageHeader'
import CyberPanel from '../components/CyberPanel'

const OP_CONFIG: Record<string, { color: string; label: string }> = {
  create: { color: '#00ff88', label: 'CREATE' },
  modify: { color: '#00f0ff', label: 'MODIFY' },
  delete: { color: '#ff3366', label: 'DELETE' },
  rename: { color: '#ffaa00', label: 'RENAME' },
}

const TIME_RANGES = [
  { value: '1h', label: '1H' },
  { value: '6h', label: '6H' },
  { value: '24h', label: '24H' },
  { value: '168h', label: '7D' },
]

const ATTRIBUTION_FILTERS = [
  { label: 'ALL', value: '', color: '#ffffff' },
  { label: 'AI', value: 'ai', color: '#00C8FF' },
  { label: 'HUMAN', value: 'none', color: '#00FF82' },
  { label: 'GIT', value: 'git', color: '#FFAA00' },
]

function formatTimeAgo(nanos: number): string {
  const diff = Date.now() - nanos / 1e6
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

export default function FilesView() {
  const [files, setFiles] = useState<FileInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [since, setSince] = useState('24h')
  const [attrFilter, setAttrFilter] = useState('')
  const health = useBelayStore((s) => s.health)
  const watcherDown = !isWatcherHealthy(health)
  const degraded = isWatcherDegraded(health)

  useEffect(() => {
    setLoading(true)
    api.getFiles(since, attrFilter || undefined)
      .then((data) => setFiles(data.files || []))
      .finally(() => setLoading(false))
  }, [since, attrFilter])

  // Group files by directory
  const grouped = files.reduce<Record<string, FileInfo[]>>((acc, f) => {
    const parts = f.path.split('/')
    const dir = parts.slice(0, -1).join('/') || '.'
    if (!acc[dir]) acc[dir] = []
    acc[dir].push(f)
    return acc
  }, {})

  const sortedDirs = Object.keys(grouped).sort()

  return (
    <div className="p-4 md:p-6 max-w-5xl font-mono">
      <CyberPageHeader
        icon={<HardDrive className="w-6 h-6" />}
        title="FILES"
        subtitle="MODIFIED FILES ACROSS ALL SESSIONS"
        right={
          <span
            className="font-mono text-xs tracking-wider"
            style={{ color: '#ffffff' }}
          >
            {files.length} FILES INDEXED
          </span>
        }
      />

      <div className="flex items-center gap-2 mb-3">
        {TIME_RANGES.map(({ value, label }) => {
          const isActive = since === value
          return (
            <button
              key={value}
              onClick={() => setSince(value)}
              className="px-4 py-1.5 font-mono text-xs tracking-widest font-bold transition-all"
              style={{
                color: isActive ? '#0a0e17' : '#00f0ff',
                background: isActive ? '#00f0ff' : 'rgba(0, 240, 255, 0.05)',
                border: `1px solid ${isActive ? '#00f0ff' : 'rgba(0, 240, 255, 0.25)'}`,
                borderRadius: '2px',
                boxShadow: isActive ? '0 0 12px rgba(0, 240, 255, 0.5), 0 0 24px rgba(0, 240, 255, 0.2)' : 'none',
                textShadow: isActive ? 'none' : '0 0 6px rgba(0, 240, 255, 0.4)',
              }}
            >
              {label}
            </button>
          )
        })}
      </div>

      <div className="flex items-center gap-2 mb-6">
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

      {loading ? (
        <div className="flex flex-col items-center justify-center py-20">
          <HardDrive
            className="w-8 h-8 mb-3"
            style={{ color: '#00f0ff', filter: 'drop-shadow(0 0 8px rgba(0, 240, 255, 0.6))' }}
          />
          <p
            className="font-mono text-sm tracking-widest"
            style={{ color: '#00f0ff', textShadow: '0 0 8px rgba(0, 240, 255, 0.4)' }}
          >
            SCANNING FILES...
          </p>
        </div>
      ) : files.length === 0 ? (
        watcherDown ? (
          <div className="flex flex-col items-center justify-center py-20">
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
                ? 'File changes may not be recorded.'
                : 'File changes are not being recorded.'}
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
              Run: <span style={{ color: degraded ? '#ffaa00' : '#ff3366' }}>thelab restart belay-api</span>
            </div>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center py-20">
            <HardDrive
              className="w-8 h-8 mb-3"
              style={{ color: 'rgba(0, 240, 255, 0.4)' }}
            />
            <p
              className="font-mono text-sm tracking-widest"
              style={{ color: '#ffffff' }}
            >
              NO FILES MODIFIED
            </p>
            <p
              className="font-mono text-xs tracking-wider mt-1"
              style={{ color: 'rgba(0, 240, 255, 0.7)' }}
            >
              No file changes detected in this time range
            </p>
          </div>
        )
      ) : (
        <div className="space-y-5">
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
                  ? 'New file changes may not be recorded.'
                  : 'New file changes will not be recorded until the watcher is restarted.'}
              </span>
            </div>
          )}
          {sortedDirs.map((dir) => (
            <CyberPanel key={dir}>
              {/* Directory header */}
              <div
                className="font-mono text-xs tracking-wider font-bold mb-3"
                style={{
                  color: '#00f0ff',
                  textShadow: '0 0 6px rgba(0, 240, 255, 0.4)',
                }}
              >
                {dir}/
              </div>

              <div className="cyber-divider mb-3" />

              {/* File rows */}
              <div className="space-y-0">
                {grouped[dir].map((f) => {
                  const fileName = f.path.split('/').pop()
                  const op = f.last_op?.toLowerCase() || 'modify'
                  const config = OP_CONFIG[op] || { color: '#888888', label: op.toUpperCase().slice(0, 6) }

                  return (
                    <Link
                      key={f.path}
                      to={`/files/${encodeURIComponent(f.path)}`}
                      className="cyber-event-row flex items-center gap-3 px-3 py-2.5 text-xs transition-all"
                      style={{ borderBottom: '1px solid rgba(0, 240, 255, 0.06)' }}
                    >
                      {/* Op badge */}
                      <span
                        className="inline-flex items-center px-2 py-0.5 rounded-sm font-bold text-xs tracking-wider shrink-0 font-mono"
                        style={{
                          color: config.color,
                          background: `${config.color}15`,
                          border: `1px solid ${config.color}30`,
                          textShadow: `0 0 6px ${config.color}60`,
                        }}
                      >
                        {config.label}
                      </span>

                      {/* File name */}
                      <span
                        className="flex-1 min-w-0 truncate font-mono font-medium"
                        style={{ color: '#e0e8ff' }}
                      >
                        {fileName}
                      </span>

                      {/* Session ID badge */}
                      {f.session_id && (
                        <span
                          className="shrink-0 px-2 py-0.5 rounded-sm font-mono text-xs tracking-wider"
                          style={{
                            color: '#00f0ff',
                            background: 'rgba(0, 240, 255, 0.08)',
                            border: '1px solid rgba(0, 240, 255, 0.15)',
                          }}
                        >
                          {f.session_id.slice(0, 8)}
                        </span>
                      )}

                      {/* Event count */}
                      <span
                        className="shrink-0 font-mono text-xs tabular-nums font-bold"
                        style={{
                          color: '#ff00e5',
                          textShadow: '0 0 4px rgba(255, 0, 229, 0.4)',
                        }}
                      >
                        {f.events}x
                      </span>

                      {/* Time ago */}
                      <span
                        className="shrink-0 w-16 text-right font-mono text-xs tabular-nums"
                        style={{ color: '#ffffff' }}
                      >
                        {formatTimeAgo(f.last_time)}
                      </span>
                    </Link>
                  )
                })}
              </div>
            </CyberPanel>
          ))}
        </div>
      )}
    </div>
  )
}
