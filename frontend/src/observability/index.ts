export type { LogEntry, LogLevel, ObservabilityConfig } from './types';
export { LOG_LEVEL_SEVERITY } from './types';
export { ObservabilityLogger } from './logger';
export { LogTransport } from './transport';
export { setupErrorCapture } from './errorCapture';

import type { ObservabilityConfig } from './types';
import { ObservabilityLogger } from './logger';
import { LogTransport } from './transport';
import { setupErrorCapture } from './errorCapture';

export function initObservability(config: ObservabilityConfig): ObservabilityLogger {
  const logger = new ObservabilityLogger(config);
  const transport = new LogTransport(config);

  logger.onEntry = (entry) => transport.enqueue(entry);

  setupErrorCapture(logger);

  if (typeof window !== 'undefined') {
    (window as unknown as Record<string, unknown>).__observatory = {
      logger,
      transport,
      getBuffer: () => logger.getBuffer(),
      flush: () => transport.flush(),
    };
  }

  return logger;
}
