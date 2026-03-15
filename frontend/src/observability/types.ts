export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

export interface LogEntry {
  timestamp: string;
  level: LogLevel;
  service: string;
  module: string;
  message: string;
  request_id?: string | null;
  correlation_id?: string | null;
  error?: string;
  stack_trace?: string;
  meta?: Record<string, unknown>;
}

export interface ObservabilityConfig {
  service: string;

  endpoint?: string;

  bufferSize?: number;

  flushInterval?: number;

  minRemoteLevel?: LogLevel;

  enableConsole?: boolean;
}

export const LOG_LEVEL_SEVERITY: Record<LogLevel, number> = {
  debug: 0,
  info: 1,
  warn: 2,
  error: 3,
};
