import type { DocumentTimeline } from "../../api/contracts";
import { abortError, isAborted, isAbortError } from "../../app/abort";

export interface TimelinePollOptions {
  signal: AbortSignal;
  intervalMs: number;
  maxIntervalMs: number;
  fetchTimeline: (signal: AbortSignal) => Promise<DocumentTimeline>;
  isTerminal: (timeline: DocumentTimeline) => boolean;
  onTimeline: (timeline: DocumentTimeline) => void;
  onError: (error: unknown) => void;
}

function settleWithAbort<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) return Promise.reject(abortError());
  return new Promise<T>((resolve, reject) => {
    let settled = false;
    const finish = (action: () => void) => {
      if (settled) return;
      settled = true;
      signal.removeEventListener("abort", onAbort);
      action();
    };
    const onAbort = () => {
      finish(() => {
        reject(abortError());
      });
    };
    signal.addEventListener("abort", onAbort);
    promise.then(
      (value) => {
        finish(() => {
          resolve(value);
        });
      },
      (error: unknown) => {
        finish(() => {
          reject(error instanceof Error ? error : new Error("The timeline request failed."));
        });
      },
    );
  });
}

export function startTimelinePoll(options: TimelinePollOptions): () => void {
  let stopped = false;
  const isStopped = () => stopped;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let delay = options.intervalMs;
  const childControllers = new Set<AbortController>();

  const clearTimer = () => {
    if (timer !== undefined) {
      clearTimeout(timer);
      timer = undefined;
    }
  };

  const stop = () => {
    stopped = true;
    clearTimer();
    for (const child of childControllers) child.abort();
    childControllers.clear();
  };

  const schedule = (ms: number) => {
    if (isStopped() || isAborted(options.signal)) return;
    clearTimer();
    timer = setTimeout(() => {
      timer = undefined;
      void run();
    }, ms);
  };

  const run = async () => {
    if (isStopped() || isAborted(options.signal)) return;
    const child = new AbortController();
    childControllers.add(child);
    try {
      const timeline = await settleWithAbort(
        options.fetchTimeline(child.signal),
        child.signal,
      );
      if (isStopped() || isAborted(options.signal) || isAborted(child.signal)) return;
      options.onTimeline(timeline);
      if (options.isTerminal(timeline)) {
        stopped = true;
        return;
      }
      delay = options.intervalMs;
      schedule(delay);
    } catch (error) {
      if (isStopped() || isAborted(options.signal) || isAborted(child.signal) || isAbortError(error)) {
        return;
      }
      options.onError(error);
      delay = Math.min(delay * 2, options.maxIntervalMs);
      schedule(delay);
    } finally {
      childControllers.delete(child);
    }
  };

  if (options.signal.aborted) return stop;
  options.signal.addEventListener("abort", stop, { once: true });
  schedule(options.intervalMs);
  return stop;
}
