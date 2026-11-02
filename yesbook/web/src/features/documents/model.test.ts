import { describe, expect, it } from "vitest";
import { ApiError } from "../../api/client";
import {
  automaticImagesTimeline,
  blockedTimeline,
  firstDocumentPage,
  hashMatchTimeline,
  processingTimeline,
  readyTimeline,
  secondDocumentPage,
} from "../../fixtures/documents";
import {
  collectPathDecisions,
  derivePipelineView,
  extractionModeLabel,
  extractorPathFor,
  mergeDocuments,
  timelineSummary,
  toDocumentListState,
} from "./model";

describe("document view model", () => {
  it("maps wire events onto the diagram pipeline and derives Route", () => {
    const blocked = derivePipelineView(blockedTimeline);
    expect(blocked.steps.map((step) => step.id)).toEqual([
      "verify",
      "prepare",
      "route",
      "extract",
      "validate",
      "assemble",
      "outcome",
    ]);
    expect(blocked.steps.find((step) => step.id === "extract")).toMatchObject({
      current: true,
      statusLabel: "Failed",
      retry: 1,
      message: "extractor timed out while reading page 4",
      workflowId: "workflow-blocked",
      runId: "run-blocked-2",
    });
    expect(blocked.steps.find((step) => step.id === "route")).toMatchObject({
      statusLabel: "Completed",
    });
    expect(blocked.steps.find((step) => step.id === "route")?.message).toContain("PDFium");
    expect(blocked.steps.find((step) => step.id === "route")?.message).toContain("Docling");
    expect(blocked.steps.find((step) => step.id === "route")?.message).toContain("Managed");
    expect(blocked.steps.find((step) => step.id === "outcome")).toMatchObject({
      statusLabel: "Blocked",
      state: "outcome_blocked",
    });
    expect(blocked.retryLoop).toBe(true);
    expect(blocked.hashMatch).toBe(false);

    const processing = derivePipelineView(processingTimeline);
    expect(processing.steps.find((step) => step.id === "extract")).toMatchObject({
      current: true,
      statusLabel: "In progress",
      progressLabel: "4 of 12 pages",
    });
    expect(processing.steps.find((step) => step.id === "route")?.statusLabel).toBe("Completed");
    expect(processing.steps.find((step) => step.id === "extract")?.extractors[0]?.pathLabel).toBe(
      "Docling",
    );
  });

  it("shows the hash-match shortcut and skips later stages", () => {
    const view = derivePipelineView(hashMatchTimeline);
    expect(view.hashMatch).toBe(true);
    expect(view.hashMatchMessage).toBe("Hash match: return the existing version.");
    expect(view.steps.find((step) => step.id === "prepare")?.statusLabel).toBe("Skipped");
    expect(view.steps.find((step) => step.id === "route")).toMatchObject({
      statusLabel: "Skipped",
      reasonCode: "hash_match",
    });
    expect(view.steps.find((step) => step.id === "extract")?.statusLabel).toBe("Skipped");
    expect(view.steps.find((step) => step.id === "outcome")).toMatchObject({
      statusLabel: "Ready",
      state: "outcome_ready",
    });

    const byCodeOnly = structuredClone(hashMatchTimeline);
    for (const event of byCodeOnly.events) {
      delete event.message;
      delete event.reason_message;
      if (event.stage === "cache_lookup" && event.event === "completed") {
        event.reason_code = "hash_match";
      }
    }
    expect(derivePipelineView(byCodeOnly).hashMatch).toBe(true);
  });

  it("surfaces automatic path decisions with stable codes", () => {
    const decisions = collectPathDecisions(automaticImagesTimeline);
    expect(decisions.map((item) => item.kind)).toEqual([
      "chosen",
      "declined",
      "skipped",
      "chosen",
      "chosen",
    ]);
    expect(decisions.find((item) => item.reasonCode === "figures_missing")).toMatchObject({
      kind: "declined",
      extractor: "pdfium",
      reasonMessage: "embedded images were not represented as figures",
    });
    expect(decisions.find((item) => item.reasonCode === "extractor_unset")).toMatchObject({
      kind: "skipped",
      extractor: "docling",
    });
    expect(decisions.find((item) => item.reasonCode === "escalated")).toMatchObject({
      kind: "chosen",
      extractor: "gemini",
    });
    expect(extractionModeLabel(automaticImagesTimeline.document.extraction_mode)).toBe(
      "Automatic",
    );
    expect(extractionModeLabel("openai")).toBe("Pinned: OpenAI");
  });

  it("classifies extractor paths for the diagram", () => {
    expect(extractorPathFor("pdfium")).toBe("pdfium");
    expect(extractorPathFor("docling")).toBe("docling");
    expect(extractorPathFor("gemini")).toBe("managed");
    expect(extractorPathFor("openai")).toBe("managed");
  });

  it("summarizes active progress, hash match, and terminal failure", () => {
    expect(timelineSummary(processingTimeline)).toBe(
      "annual-report.pdf is Processing. Extract: 4 of 12 pages.",
    );
    expect(timelineSummary(blockedTimeline)).toBe(
      "field-scan.pdf is Blocked. extractor timed out while reading page 4",
    );
    expect(timelineSummary(hashMatchTimeline)).toBe(
      "duplicate-report.pdf is Ready. identical file already processed",
    );
    expect(timelineSummary(readyTimeline)).toBe("biology.pdf is Ready.");
  });

  it("keeps one row when a later page repeats a document", () => {
    const documents = mergeDocuments([firstDocumentPage, secondDocumentPage]);
    const ready = documents.filter((document) => document.id === "doc-ready");
    expect(ready).toHaveLength(1);
    expect(new Set(documents.map((document) => document.id)).size).toBe(documents.length);
    expect(documents.map((document) => document.file_name)).toEqual([
      "annual-report.pdf",
      "biology.pdf",
      "field-scan.pdf",
      "handwritten-notes.pdf",
      "damaged-pages.pdf",
      "waiting-room.pdf",
    ]);
  });

  it("maps loading, empty, and error without mixing them", () => {
    expect(
      toDocumentListState({
        pages: undefined,
        hasNextPage: false,
        isFetchingNextPage: false,
        initialError: null,
        nextPageError: null,
      }).status,
    ).toBe("loading");
    expect(
      toDocumentListState({
        pages: [{ documents: [], next_cursor: null }],
        hasNextPage: false,
        isFetchingNextPage: false,
        initialError: null,
        nextPageError: null,
      }).status,
    ).toBe("empty");
    const error = toDocumentListState({
      pages: undefined,
      hasNextPage: false,
      isFetchingNextPage: false,
      initialError: new ApiError(401, "missing or invalid API key"),
      nextPageError: null,
    });
    expect(error).toEqual({
      status: "error",
      message: "You are not authorized to do that.",
    });
  });
});
