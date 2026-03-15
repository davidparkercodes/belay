export interface BelayEvent {
  event_id: string
  file_path: string
  operation: string
  timestamp_nano: number
  session_id: string
  content_hash: string
  previous_hash: string
  content_size: number
  attribution_method: number | string
  attribution_confidence: number
  attribution: string
  metadata: Record<string, string>
}

export interface BelaySession {
  session_id: string
  tool_name: string
  pid: number
  working_directory: string
  status: string
  started_at: string
  ended_at: string
  duration: string
  label: string
  metadata: Record<string, string>
  files_changed: number
  event_count: number
}

export interface BelayConflict {
  ID: string
  FilePath: string
  Severity: number
  Sessions: string[]
  Description: string
  FirstTime: number
  LastTime: number
}

export interface FileInfo {
  path: string
  last_op: string
  last_time: number
  session_id: string
  events: number
}

export interface Stats {
  total_events: number
  total_sessions: number
  active_sessions: number
  store_bytes: number
  store_objects: number
  project_root: string
}

export interface StreamMessage {
  type: string
  timestamp: number
  data: Record<string, unknown>
}

export interface WatcherStatus {
  status: 'running' | 'stopped' | 'error' | 'degraded'
  last_event_at?: string
  error?: string
}

export interface HealthData {
  status: 'ok' | 'degraded' | 'unhealthy'
  version: string
  uptime?: string
  watcher?: WatcherStatus
}
