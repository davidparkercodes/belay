import type { LogEntry, LogLevel, ObservabilityConfig } from './types';

const SESSION_KEY = '__observatory_session_id';

function generateSessionId(): string {
  const array = new Uint8Array(16);
  crypto.getRandomValues(array);
  return Array.from(array, (b) => b.toString(16).padStart(2, '0')).join('');
}

function getSessionId(): string {
  try {
    let id = sessionStorage.getItem(SESSION_KEY);
    if (!id) {
      id = generateSessionId();
      sessionStorage.setItem(SESSION_KEY, id);
    }
    return id;
  } catch {
    return generateSessionId();
  }
}

export class ObservabilityLogger {
  private readonly buffer: (LogEntry | null)[];
  private head = 0;
  private count = 0;
  private readonly service: string;
  private readonly enableConsole: boolean;
  private readonly sessionId: string;

  onEntry: ((entry: LogEntry) => void) | null = null;

  constructor(config: ObservabilityConfig) {
    const bufferSize = config.bufferSize ?? 500;
    this.buffer = new Array<LogEntry | null>(bufferSize).fill(null);
    this.service = config.service;
    this.enableConsole = config.enableConsole ?? true;
    this.sessionId = getSessionId();
  }

  debug(module: string, message: string, meta?: Record<string, unknown>): void {
    this.log('debug', module, message, undefined, meta);
  }

  info(module: string, message: string, meta?: Record<string, unknown>): void {
    this.log('info', module, message, undefined, meta);
  }

  warn(module: string, message: string, meta?: Record<string, unknown>): void {
    this.log('warn', module, message, undefined, meta);
  }

  error(module: string, message: string, errorOrMeta?: Error | Record<string, unknown>): void {
    if (errorOrMeta instanceof Error) {
      this.log('error', module, message, errorOrMeta);
    } else {
      this.log('error', module, message, undefined, errorOrMeta);
    }
  }

  getBuffer(): LogEntry[] {
    const entries: LogEntry[] = [];
    if (this.count === 0) return entries;

    const size = this.buffer.length;
    const start = this.count < size ? 0 : this.head;
    const total = Math.min(this.count, size);

    for (let i = 0; i < total; i++) {
      const entry = this.buffer[(start + i) % size];
      if (entry) entries.push(entry);
    }
    return entries;
  }

  private log(
    level: LogLevel,
    module: string,
    message: string,
    err?: Error,
    meta?: Record<string, unknown>,
  ): void {
    const currentUrl = typeof window !== 'undefined' ? window.location.href : undefined;

    const entry: LogEntry = {
      timestamp: new Date().toISOString(),
      level,
      service: this.service,
      module,
      message,
      request_id: null,
      correlation_id: null,
      error: err ? `${err.name}: ${err.message}` : undefined,
      stack_trace: err?.stack ?? undefined,
      meta: {
        url: currentUrl,
        session_id: this.sessionId,
        user_agent: typeof navigator !== 'undefined' ? navigator.userAgent : undefined,
        ...meta,
      },
    };

    this.buffer[this.head] = entry;
    this.head = (this.head + 1) % this.buffer.length;
    this.count++;

    if (this.enableConsole) {
      const prefix = `[${this.service}:${module}]`;
      const consoleFn = level === 'debug' ? console.debug
        : level === 'info' ? console.info
        : level === 'warn' ? console.warn
        : console.error;

      if (err) {
        consoleFn(prefix, message, err);
      } else {
        consoleFn(prefix, message, meta ?? '');
      }
    }

    this.onEntry?.(entry);
  }
}
