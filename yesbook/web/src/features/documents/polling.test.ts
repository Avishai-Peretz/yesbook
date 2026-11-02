import { afterEach, describe, expect, it, vi } from "vitest";
import { isFinalDocumentStatus } from "../../api/contracts";
import { blockedTimeline, processingTimeline } from "../../fixtures/documents";
import { startTimelinePoll } from "./polling";

afterEach(() => {
  vi.useRealTimers();
});

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

describe("timeline polling", () => {
  it("does not poll again after a terminal failure", async () => {
    vi.useFakeTimers();
    let calls = 0;
    const controller = new AbortController();
    startTimelinePoll({
      signal: controller.signal,
      intervalMs: 1_000,
      maxIntervalMs: 8_000,
      fetchTimeline: () => {
        calls += 1;
        return Promise.resolve(blockedTimeline);
      },
      isTerminal: (timeline) => isFinalDocumentStatus(timeline.document.status),
      onTimeline: () => undefined,
      onError: () => undefined,
    });

    await vi.advanceTimersByTimeAsync(1_000);
    expect(calls).toBe(1);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(calls).toBe(1);
    controller.abort();
  });

  it("waits for the in-flight poll before starting another", async () => {
    vi.useFakeTimers();
    let calls = 0;
    const gate = deferred<typeof processingTimeline>();
    const controller = new AbortController();
    startTimelinePoll({
      signal: controller.signal,
      intervalMs: 1_000,
      maxIntervalMs: 8_000,
      fetchTimeline: () => {
        calls += 1;
        return gate.promise;
      },
      isTerminal: () => false,
      onTimeline: () => undefined,
      onError: () => undefined,
    });

    await vi.advanceTimersByTimeAsync(5_000);
    expect(calls).toBe(1);
    gate.resolve(processingTimeline);
    await vi.advanceTimersByTimeAsync(999);
    expect(calls).toBe(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(calls).toBe(2);
    controller.abort();
  });

  it("backs off after a failure and resets after success", async () => {
    vi.useFakeTimers();
    let calls = 0;
    let fail = true;
    const controller = new AbortController();
    startTimelinePoll({
      signal: controller.signal,
      intervalMs: 1_000,
      maxIntervalMs: 8_000,
      fetchTimeline: () => {
        calls += 1;
        if (fail) return Promise.reject(new Error("unavailable"));
        return Promise.resolve(processingTimeline);
      },
      isTerminal: () => false,
      onTimeline: () => undefined,
      onError: () => undefined,
    });

    await vi.advanceTimersByTimeAsync(1_000);
    expect(calls).toBe(1);
    await vi.advanceTimersByTimeAsync(1_999);
    expect(calls).toBe(1);
    fail = false;
    await vi.advanceTimersByTimeAsync(1);
    expect(calls).toBe(2);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(calls).toBe(3);
    controller.abort();
  });

  it("cancels the wait and the in-flight request", async () => {
    vi.useFakeTimers();
    let calls = 0;
    let seen: AbortSignal | undefined;
    const controller = new AbortController();
    const stop = startTimelinePoll({
      signal: controller.signal,
      intervalMs: 1_000,
      maxIntervalMs: 8_000,
      fetchTimeline: (signal) => {
        calls += 1;
        seen = signal;
        return new Promise(() => undefined);
      },
      isTerminal: () => false,
      onTimeline: () => undefined,
      onError: () => undefined,
    });

    controller.abort();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(calls).toBe(0);

    const second = new AbortController();
    startTimelinePoll({
      signal: second.signal,
      intervalMs: 1_000,
      maxIntervalMs: 8_000,
      fetchTimeline: (signal) => {
        calls += 1;
        seen = signal;
        return new Promise(() => undefined);
      },
      isTerminal: () => false,
      onTimeline: () => undefined,
      onError: () => undefined,
    });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(calls).toBe(1);
    stop();
    second.abort();
    expect(seen?.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(calls).toBe(1);
  });
});
