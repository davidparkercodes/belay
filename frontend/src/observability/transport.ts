import type { LogEntry, LogLevel, ObservabilityConfig } from './types';
import { LOG_LEVEL_SEVERITY } from './types';

const DEFAULT_FLUSH_INTERVAL = 10_000;
const DEFAULT_MIN_REMOTE_LEVEL: LogLevel = 'warn';
const INGEST_PATH = '/api/observatory/logs/ingest';

/**
 * Resolve the Observatory endpoint.
 * Returns the configured endpoint URL or null if Observatory is disabled.
 *
 * Priority:
 *  1. Explicit `config.endpoint`
 *  2. `VITE_OBSERVATORY_ENDPOINT` env var
 *  3. null (disabled — no-op transport)
 */
function resolveEndpoint(config: ObservabilityConfig): string | null {
  if (config.endpoint) {
    return config.endpoint;
  }

  const envEndpoint = import.meta.env.VITE_OBSERVATORY_ENDPOINT as string | undefined;
  if (envEndpoint) {
    // Env var may be a bare origin (http://host:port) or a full URL
    return envEndpoint.endsWith(INGEST_PATH) ? envEndpoint : `${envEndpoint}${INGEST_PATH}`;
  }

  // No endpoint configured — disable Observatory logging
  return null;
}

export class LogTransport {
  private queue: LogEntry[] = [];
  private timer: ReturnType<typeof setInterval> | null = null;
  private readonly endpoint: string | null;
  private readonly minSeverity: number;
  readonly enabled: boolean;

  constructor(config: ObservabilityConfig) {
    const minLevel = config.minRemoteLevel ?? DEFAULT_MIN_REMOTE_LEVEL;
    this.minSeverity = LOG_LEVEL_SEVERITY[minLevel];
    this.endpoint = resolveEndpoint(config);
    this.enabled = this.endpoint !== null;

    // If no endpoint, skip all timers and listeners (no-op mode)
    if (!this.enabled) return;

    const flushInterval = config.flushInterval ?? DEFAULT_FLUSH_INTERVAL;
    this.timer = setInterval(() => this.flush(), flushInterval);

    if (typeof window !== 'undefined') {
      window.addEventListener('beforeunload', () => this.flush());
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'hidden') this.flush();
      });
    }
  }

  enqueue(entry: LogEntry): void {
    if (!this.enabled) return;

    const severity = LOG_LEVEL_SEVERITY[entry.level] ?? 0;
    if (severity >= this.minSeverity) {
      this.queue.push(entry);
    }
  }

  flush(): void {
    if (!this.enabled || this.queue.length === 0) return;

    const batch = this.queue.splice(0);
    const payload = JSON.stringify({ entries: batch });

    try {
      if (typeof navigator !== 'undefined' && navigator.sendBeacon) {
        const blob = new Blob([payload], { type: 'application/json' });
        const sent = navigator.sendBeacon(this.endpoint!, blob);
        if (sent) return;
      }

      if (typeof fetch !== 'undefined') {
        fetch(this.endpoint!, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: payload,
          keepalive: true,
        }).catch(() => {});
      }
    } catch {
    }
  }

  destroy(): void {
    if (this.timer !== null) {
      clearInterval(this.timer);
      this.timer = null;
    }
    if (this.enabled) {
      this.flush();
    }
  }
}
