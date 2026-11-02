import { describe, expect, it } from "vitest";
import {
  STAGE_EVENT_TYPES,
  STAGE_NAMES,
  isFinalDocumentStatus,
  progressMetadataSchema,
  stageEventSchema,
} from "../api/contracts";
import {
  blockedTimeline,
  hashMatchTimeline,
  partialTimeline,
  processingTimeline,
  queuedTimeline,
  readyTimeline,
  reviewTimeline,
} from "./documents";

const timelines = [
  processingTimeline,
  readyTimeline,
  blockedTimeline,
  reviewTimeline,
  partialTimeline,
  queuedTimeline,
  hashMatchTimeline,
];

describe("progress fixtures", () => {
  it("covers every stage and event from the processing contract", () => {
    const events = timelines.flatMap((timeline) => timeline.events);
    expect(new Set(events.map((event) => event.stage))).toEqual(new Set(STAGE_NAMES));
    expect(new Set(events.map((event) => event.event))).toEqual(new Set(STAGE_EVENT_TYPES));
    expect(blockedTimeline.events.some((event) => event.retry === 1 && event.event === "failed")).toBe(
      true,
    );
    expect(isFinalDocumentStatus(blockedTimeline.document.status)).toBe(true);
    expect(isFinalDocumentStatus(processingTimeline.document.status)).toBe(false);
  });

  it("rejects a progress event that omits progress metadata", () => {
    const sample = processingTimeline.events.find((event) => event.event === "progress");
    if (!sample?.progress) throw new Error("processing fixture is missing progress");
    expect(sample.progress).toEqual({ current: 4, total: 12, unit: "pages" });
    expect(stageEventSchema.safeParse({ ...sample, progress: undefined }).success).toBe(false);
  });

  it("rejects progress that moves past the total", () => {
    expect(
      progressMetadataSchema.safeParse({ current: 4, total: 3, unit: "pages" }).success,
    ).toBe(false);
  });
});
