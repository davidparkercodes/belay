import type { BelayEvent } from '../types'
import { Link } from 'react-router-dom'

const OP_CONFIG: Record<string, { color: string; label: string }> = {
  create: { color: '#00ff88', label: 'CREATE' },
  modify: { color: '#00f0ff', label: 'MODIFY' },
  delete: { color: '#ff3366', label: 'DELETE' },
  rename: { color: '#ffaa00', label: 'RENAME' },
  chmod: { color: '#888888', label: 'CHMOD' },
}

const ATTRIBUTION_CONFIG = {
  ai: { color: '#00C8FF', label: 'AI', border: 'rgba(0, 200, 255, 0.4)', bg: 'rgba(0, 200, 255, 0.1)' },
  human: { color: '#00FF82', label: 'HUMAN', border: 'rgba(0, 255, 130, 0.4)', bg: 'rgba(0, 255, 130, 0.1)' },
  git: { color: '#FFAA00', label: 'GIT', border: 'rgba(255, 170, 0, 0.4)', bg: 'rgba(255, 170, 0, 0.1)' },
}

function getAttributionType(event: BelayEvent): 'ai' | 'human' | 'git' {
  const method = typeof event.attribution_method === 'string'
    ? parseInt(event.attribution_method, 10)
    : event.attribution_method
  if (method === 6) return 'git'
  if (method >= 1 && method <= 5 && event.session_id) return 'ai'
  return 'human'
}

function parseGitOperation(filePath: string): string | null {
  if (filePath.startsWith('[git:') && filePath.endsWith(']')) {
    return filePath.slice(5, -1)
  }
  return null
}

function formatTimeAgo(nanos: number): string {
  const ms = nanos / 1e6
  const diff = Date.now() - ms
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

function formatSize(bytes: number): string {
  if (bytes === 0) return '-'
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)}K`
  return `${(bytes / 1048576).toFixed(1)}M`
}

interface Props {
  event: BelayEvent
  showFile?: boolean
  showSession?: boolean
}

export default function EventRow({ event, showFile = true, showSession = true }: Props) {
  const op = event.operation?.toLowerCase() || 'modify'
  const config = OP_CONFIG[op] || { color: '#888888', label: op.toUpperCase().slice(0, 6) }
  const attrType = getAttributionType(event)
  const attrConfig = ATTRIBUTION_CONFIG[attrType]
  const gitOp = parseGitOperation(event.file_path)

  const fileName = gitOp ? null : (event.file_path?.split('/').pop() || event.file_path)
  const dirPath = gitOp ? null : (event.file_path?.split('/').slice(0, -1).join('/') || '')

  return (
    <div
      className="cyber-event-row flex items-center gap-3 px-3 py-2.5 font-mono text-xs"
      style={{ borderBottom: '1px solid rgba(0, 240, 255, 0.06)' }}
    >
      <span
        className="inline-flex items-center px-2 py-0.5 rounded-sm font-bold text-xs tracking-wider shrink-0"
        style={{
          color: config.color,
          background: `${config.color}15`,
          border: `1px solid ${config.color}30`,
          textShadow: `0 0 6px ${config.color}60`,
          animation: 'badge-glow 3s ease-in-out infinite',
        }}
      >
        {config.label}
      </span>

      <span
        className="inline-flex items-center px-1.5 py-0.5 rounded-sm font-bold tracking-wider shrink-0"
        style={{
          fontSize: 12,
          color: attrConfig.color,
          background: attrConfig.bg,
          border: `1px solid ${attrConfig.border}`,
          textShadow: `0 0 4px ${attrConfig.color}60`,
        }}
      >
        {attrConfig.label}
      </span>

      {showFile && (
        gitOp ? (
          <span
            className="flex-1 min-w-0 truncate font-bold"
            style={{ color: '#FFAA00', textShadow: '0 0 4px rgba(255, 170, 0, 0.4)' }}
            title={event.file_path}
          >
            git {gitOp}
          </span>
        ) : (
          <Link
            to={`/files/${encodeURIComponent(event.file_path)}`}
            className="flex-1 min-w-0 truncate font-medium transition-colors"
            style={{ color: '#e0e8ff' }}
            title={event.file_path}
          >
            {dirPath && (
              <span style={{ color: 'rgba(0, 240, 255, 0.5)' }}>{dirPath}/</span>
            )}
            {fileName}
          </Link>
        )
      )}

      {showSession && event.session_id && (
        <Link
          to={`/sessions/${event.session_id}`}
          className="shrink-0 px-2 py-0.5 rounded-sm tracking-wider text-xs transition-colors"
          style={{
            color: '#00f0ff',
            background: 'rgba(0, 240, 255, 0.08)',
            border: '1px solid rgba(0, 240, 255, 0.15)',
          }}
          title={event.session_id}
        >
          {event.session_id.slice(0, 8)}
        </Link>
      )}

      <span
        className="shrink-0 w-12 text-right tabular-nums"
        style={{ color: '#ffffff' }}
      >
        {formatSize(event.content_size)}
      </span>

      <span
        className="shrink-0 w-16 text-right tabular-nums"
        style={{ color: '#ffffff' }}
        title={new Date(event.timestamp_nano / 1e6).toLocaleString()}
      >
        {formatTimeAgo(event.timestamp_nano)}
      </span>
    </div>
  )
}
