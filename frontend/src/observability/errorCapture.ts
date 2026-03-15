import type { ObservabilityLogger } from './logger';

const MODULE = 'error-capture';

export function setupErrorCapture(logger: ObservabilityLogger): () => void {
  if (typeof window === 'undefined') return () => {};

  const handleError = (event: ErrorEvent): void => {
    const err = event.error;
    if (err instanceof Error) {
      logger.error(MODULE, `Uncaught exception: ${err.message}`, err);
    } else {
      logger.error(MODULE, `Uncaught exception: ${String(event.message)}`, {
        filename: event.filename,
        lineno: event.lineno,
        colno: event.colno,
      });
    }
  };

  const handleRejection = (event: PromiseRejectionEvent): void => {
    const reason = event.reason;
    if (reason instanceof Error) {
      logger.error(MODULE, `Unhandled promise rejection: ${reason.message}`, reason);
    } else {
      logger.error(MODULE, `Unhandled promise rejection: ${String(reason)}`);
    }
  };

  window.addEventListener('error', handleError);
  window.addEventListener('unhandledrejection', handleRejection);

  return () => {
    window.removeEventListener('error', handleError);
    window.removeEventListener('unhandledrejection', handleRejection);
  };
}
