import {
  documentListSchema,
  documentSchema,
  documentTimelineSchema,
  type Document,
  type DocumentList,
  type DocumentTimeline,
  type StageEventType,
  type StageName,
  STAGE_NAMES,
} from "../api/contracts";

// JSON field names match processing.ProgressEvent in internal/processing/progress.go:
// document_id, workflow_id, run_id, stage, event, retry, progress, message,
// reason_code, reason_message, occurred_at.

interface EventInput {
  id: string;
  documentId: string;
  stage: StageName;
  event: StageEventType;
  occurredAt: string;
  workflowId: string;
  runId: string;
  retry?: number;
  progress?: { current: number; total: number; unit: string };
  message?: string;
  reasonCode?: string;
  reasonMessage?: string;
}

function event(input: EventInput) {
  return {
    id: input.id,
    document_id: input.documentId,
    workflow_id: input.workflowId,
    run_id: input.runId,
    stage: input.stage,
    event: input.event,
    retry: input.retry ?? 0,
    occurred_at: input.occurredAt,
    ...(input.progress ? { progress: input.progress } : {}),
    ...(input.message ? { message: input.message } : {}),
    ...(input.reasonCode ? { reason_code: input.reasonCode } : {}),
    ...(input.reasonMessage ? { reason_message: input.reasonMessage } : {}),
  };
}

function doc(input: {
  id: string;
  status: Document["status"];
  fileName: string;
  sizeBytes: number;
  createdAt: string;
  updatedAt: string;
  versionId?: string;
  reason?: string;
  extractionMode?: Document["extraction_mode"];
}): Document {
  return documentSchema.parse({
    id: input.id,
    status: input.status,
    file_name: input.fileName,
    size_bytes: input.sizeBytes,
    created_at: input.createdAt,
    updated_at: input.updatedAt,
    ...(input.versionId ? { version_id: input.versionId } : {}),
    ...(input.reason ? { reason: input.reason } : {}),
    ...(input.extractionMode ? { extraction_mode: input.extractionMode } : {}),
  });
}

function completedPipeline(
  documentId: string,
  workflowId: string,
  runId: string,
  prefix: string,
) {
  return STAGE_NAMES.map((stage, index) =>
    event({
      id: `${prefix}-${stage}`,
      documentId,
      stage,
      event: "completed",
      occurredAt: `2026-11-02T15:${String(index).padStart(2, "0")}:00Z`,
      workflowId,
      runId,
    }),
  );
}

export const processingDocument = doc({
  id: "doc-processing",
  status: "processing",
  fileName: "annual-report.pdf",
  sizeBytes: 2_408_112,
  createdAt: "2026-11-02T13:00:00Z",
  updatedAt: "2026-11-02T13:04:15Z",
});

export const readyDocument = doc({
  id: "doc-ready",
  status: "ready",
  fileName: "biology.pdf",
  sizeBytes: 184_320,
  createdAt: "2026-11-02T12:00:00Z",
  updatedAt: "2026-11-02T12:08:00Z",
  versionId: "ver-ready",
});

export const automaticImagesDocument = doc({
  id: "doc-automatic-images",
  status: "ready",
  fileName: "cv-with-photo.pdf",
  sizeBytes: 512_000,
  createdAt: "2026-11-02T15:30:00Z",
  updatedAt: "2026-11-02T15:38:00Z",
  versionId: "ver-automatic-images",
  extractionMode: "automatic",
});

export const blockedDocument = doc({
  id: "doc-blocked",
  status: "blocked",
  fileName: "field-scan.pdf",
  sizeBytes: 4_096,
  createdAt: "2026-11-02T11:00:00Z",
  updatedAt: "2026-11-02T11:06:40Z",
  reason: "extractor timed out while reading page 4",
});

export const reviewDocument = doc({
  id: "doc-review",
  status: "needs_review",
  fileName: "handwritten-notes.pdf",
  sizeBytes: 980_000,
  createdAt: "2026-11-02T10:00:00Z",
  updatedAt: "2026-11-02T10:12:00Z",
  versionId: "ver-review",
  reason: "1 of 4 pages flagged for review",
});

export const partialDocument = doc({
  id: "doc-partial",
  status: "partial",
  fileName: "damaged-pages.pdf",
  sizeBytes: 640_000,
  createdAt: "2026-11-02T09:00:00Z",
  updatedAt: "2026-11-02T09:11:00Z",
  versionId: "ver-partial",
  reason: "2 of 4 pages could not be extracted",
});

export const queuedDocument = doc({
  id: "doc-queued",
  status: "queued",
  fileName: "waiting-room.pdf",
  sizeBytes: 88_000,
  createdAt: "2026-11-02T16:00:00Z",
  updatedAt: "2026-11-02T16:00:05Z",
});

export const processingTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: processingDocument,
  events: [
    event({
      id: "evt-processing-verify",
      documentId: processingDocument.id,
      stage: "verify",
      event: "completed",
      occurredAt: "2026-11-02T13:01:00Z",
      workflowId: "workflow-processing",
      runId: "run-processing",
    }),
    event({
      id: "evt-processing-cache",
      documentId: processingDocument.id,
      stage: "cache_lookup",
      event: "completed",
      occurredAt: "2026-11-02T13:01:05Z",
      workflowId: "workflow-processing",
      runId: "run-processing",
    }),
    event({
      id: "evt-processing-prepare",
      documentId: processingDocument.id,
      stage: "prepare",
      event: "completed",
      occurredAt: "2026-11-02T13:01:20Z",
      workflowId: "workflow-processing",
      runId: "run-processing",
    }),
    event({
      id: "evt-processing-extract-started",
      documentId: processingDocument.id,
      stage: "extract",
      event: "started",
      occurredAt: "2026-11-02T13:02:00Z",
      workflowId: "workflow-processing",
      runId: "run-processing",
    }),
    event({
      id: "evt-processing-extract-progress",
      documentId: processingDocument.id,
      stage: "extract",
      event: "progress",
      occurredAt: "2026-11-02T13:04:15Z",
      workflowId: "workflow-processing",
      runId: "run-processing",
      progress: { current: 4, total: 12, unit: "pages" },
    }),
  ],
  attempts: [
    {
      id: "attempt-processing-1",
      document_id: processingDocument.id,
      target_id: "target-processing",
      extractor: "docling",
      extractor_version: "2",
      attempt: 1,
      status: "started",
      cost_micros: 0,
      started_at: "2026-11-02T13:02:00Z",
    },
  ],
});

export const readyTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: readyDocument,
  events: completedPipeline(
    readyDocument.id,
    "workflow-ready",
    "run-ready",
    "evt-ready",
  ),
  attempts: [
    {
      id: "attempt-ready-1",
      document_id: readyDocument.id,
      target_id: "target-ready",
      extractor: "pdfium",
      extractor_version: "1",
      attempt: 1,
      status: "succeeded",
      cost_micros: 250_000,
      started_at: "2026-11-02T12:02:00Z",
      completed_at: "2026-11-02T12:07:30Z",
      decision: "chosen",
      reason_code: "validation_accept",
      reason_message: "coverage+structure+figures ok",
    },
  ],
  decisions: [
    {
      kind: "chosen",
      extractor: "pdfium",
      target_id: "target-ready",
      reason_code: "chain_first",
      reason_message: "content_text chain: first extractor pdfium",
      occurred_at: "2026-11-02T12:02:00Z",
    },
    {
      kind: "chosen",
      extractor: "pdfium",
      target_id: "target-ready",
      reason_code: "validation_accept",
      reason_message: "coverage+structure+figures ok",
      occurred_at: "2026-11-02T12:07:30Z",
    },
  ],
});

export const automaticImagesTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: automaticImagesDocument,
  events: completedPipeline(
    automaticImagesDocument.id,
    "workflow-automatic-images",
    "run-automatic-images",
    "evt-automatic-images",
  ),
  attempts: [
    {
      id: "attempt-auto-img-1",
      document_id: automaticImagesDocument.id,
      target_id: "target-auto-img",
      extractor: "pdfium",
      extractor_version: "1",
      attempt: 1,
      status: "failed",
      cost_micros: 90_000,
      started_at: "2026-11-02T15:32:00Z",
      completed_at: "2026-11-02T15:33:00Z",
      decision: "declined",
      reason_code: "figures_missing",
      reason_message: "embedded images present, no figure blocks",
      error: "figures_missing",
    },
    {
      id: "attempt-auto-img-2",
      document_id: automaticImagesDocument.id,
      target_id: "target-auto-img",
      extractor: "docling",
      extractor_version: "2",
      attempt: 2,
      status: "failed",
      cost_micros: 0,
      started_at: "2026-11-02T15:33:10Z",
      completed_at: "2026-11-02T15:33:10Z",
      decision: "skipped",
      reason_code: "extractor_unset",
      reason_message: "docling url unset; continuing Automatic escalate without it",
      error: "extractor_unset",
    },
    {
      id: "attempt-auto-img-3",
      document_id: automaticImagesDocument.id,
      target_id: "target-auto-img",
      extractor: "gemini",
      extractor_version: "1",
      attempt: 3,
      status: "succeeded",
      cost_micros: 520_000,
      started_at: "2026-11-02T15:33:20Z",
      completed_at: "2026-11-02T15:37:40Z",
      decision: "chosen",
      reason_code: "escalated",
      reason_message: "escalating to gemini",
    },
  ],
  decisions: [
    {
      kind: "chosen",
      extractor: "pdfium",
      target_id: "target-auto-img",
      reason_code: "chain_first",
      reason_message: "starting Automatic chain with pdfium",
      occurred_at: "2026-11-02T15:32:00Z",
    },
    {
      kind: "declined",
      extractor: "pdfium",
      target_id: "target-auto-img",
      reason_code: "figures_missing",
      reason_message: "embedded images were not represented as figures",
      occurred_at: "2026-11-02T15:33:00Z",
    },
    {
      kind: "skipped",
      extractor: "docling",
      target_id: "target-auto-img",
      reason_code: "extractor_unset",
      reason_message: "docling url unset; continuing Automatic escalate without it",
      occurred_at: "2026-11-02T15:33:10Z",
    },
    {
      kind: "chosen",
      extractor: "gemini",
      target_id: "target-auto-img",
      reason_code: "escalated",
      reason_message: "escalating to gemini",
      occurred_at: "2026-11-02T15:33:20Z",
    },
    {
      kind: "chosen",
      extractor: "gemini",
      target_id: "target-auto-img",
      reason_code: "validation_accept",
      reason_message: "figure asset accepted after escalation",
      occurred_at: "2026-11-02T15:37:40Z",
    },
  ],
});

export const figuresReviewDocument = doc({
  id: "doc-figures-review",
  status: "needs_review",
  fileName: "cv-photo-only-pdfium.pdf",
  sizeBytes: 420_000,
  createdAt: "2026-11-02T16:00:00Z",
  updatedAt: "2026-11-02T16:12:00Z",
  versionId: "ver-figures-review",
  extractionMode: "automatic",
  reason: "1 of 1 pages flagged for review",
});

export const figuresReviewTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: figuresReviewDocument,
  events: completedPipeline(
    figuresReviewDocument.id,
    "workflow-figures-review",
    "run-figures-review",
    "evt-figures-review",
  ),
  attempts: [
    {
      id: "attempt-figures-review-1",
      document_id: figuresReviewDocument.id,
      target_id: "target-figures-review",
      extractor: "pdfium",
      extractor_version: "1",
      attempt: 1,
      status: "succeeded",
      cost_micros: 40_000,
      started_at: "2026-11-02T16:10:00Z",
      completed_at: "2026-11-02T16:11:00Z",
      decision: "declined",
      reason_code: "figures_missing",
      reason_message: "embedded images were not represented as figures",
    },
  ],
  decisions: [
    {
      kind: "chosen",
      extractor: "pdfium",
      target_id: "target-figures-review",
      reason_code: "chain_first",
      reason_message: "starting Automatic chain with pdfium",
      occurred_at: "2026-11-02T16:10:00Z",
    },
    {
      kind: "declined",
      extractor: "pdfium",
      target_id: "target-figures-review",
      reason_code: "figures_missing",
      reason_message: "embedded images were not represented as figures",
      occurred_at: "2026-11-02T16:11:00Z",
    },
    {
      kind: "skipped",
      extractor: "docling",
      reason_code: "extractor_unset",
      reason_message: "docling url unset; continuing Automatic escalate without it",
      occurred_at: "2026-11-02T16:11:05Z",
    },
    {
      kind: "skipped",
      reason_code: "chain_exhausted",
      reason_message: "no further extractors configured after pdfium; embedded images were not represented as figures",
      occurred_at: "2026-11-02T16:11:10Z",
    },
  ],
});

const blockedReason = "extractor timed out while reading page 4";

export const blockedTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: blockedDocument,
  events: [
    event({
      id: "evt-blocked-verify-started",
      documentId: blockedDocument.id,
      stage: "verify",
      event: "started",
      occurredAt: "2026-11-02T11:01:00Z",
      workflowId: "workflow-blocked",
      runId: "run-blocked-1",
    }),
    event({
      id: "evt-blocked-verify",
      documentId: blockedDocument.id,
      stage: "verify",
      event: "completed",
      occurredAt: "2026-11-02T11:01:20Z",
      workflowId: "workflow-blocked",
      runId: "run-blocked-1",
    }),
    event({
      id: "evt-blocked-cache",
      documentId: blockedDocument.id,
      stage: "cache_lookup",
      event: "completed",
      occurredAt: "2026-11-02T11:01:25Z",
      workflowId: "workflow-blocked",
      runId: "run-blocked-1",
    }),
    event({
      id: "evt-blocked-prepare",
      documentId: blockedDocument.id,
      stage: "prepare",
      event: "completed",
      occurredAt: "2026-11-02T11:02:00Z",
      workflowId: "workflow-blocked",
      runId: "run-blocked-1",
    }),
    event({
      id: "evt-blocked-extract-started",
      documentId: blockedDocument.id,
      stage: "extract",
      event: "started",
      occurredAt: "2026-11-02T11:03:00Z",
      workflowId: "workflow-blocked",
      runId: "run-blocked-1",
    }),
    event({
      id: "evt-blocked-extract-failed",
      documentId: blockedDocument.id,
      stage: "extract",
      event: "failed",
      occurredAt: "2026-11-02T11:06:40Z",
      workflowId: "workflow-blocked",
      runId: "run-blocked-2",
      retry: 1,
      message: blockedReason,
    }),
  ],
  attempts: [
    {
      id: "attempt-blocked-1",
      document_id: blockedDocument.id,
      target_id: "target-blocked",
      extractor: "pdfium",
      extractor_version: "1",
      attempt: 1,
      status: "failed",
      cost_micros: 125_000,
      started_at: "2026-11-02T11:03:00Z",
      completed_at: "2026-11-02T11:04:00Z",
      error: "extractor timed out",
    },
    {
      id: "attempt-blocked-2",
      document_id: blockedDocument.id,
      target_id: "target-blocked",
      extractor: "docling",
      extractor_version: "2",
      attempt: 2,
      status: "failed",
      cost_micros: 80_000,
      started_at: "2026-11-02T11:05:00Z",
      completed_at: "2026-11-02T11:06:40Z",
      error: blockedReason,
    },
    {
      id: "attempt-blocked-3",
      document_id: blockedDocument.id,
      target_id: "target-blocked",
      extractor: "gemini",
      extractor_version: "1",
      attempt: 3,
      status: "failed",
      cost_micros: 40_000,
      started_at: "2026-11-02T11:06:00Z",
      completed_at: "2026-11-02T11:06:40Z",
      error: blockedReason,
    },
  ],
});

const hashMatchReason = "identical file already processed";

export const hashMatchDocument = doc({
  id: "doc-hash-match",
  status: "ready",
  fileName: "duplicate-report.pdf",
  sizeBytes: 2_408_112,
  createdAt: "2026-11-02T14:00:00Z",
  updatedAt: "2026-11-02T14:00:40Z",
  versionId: "ver-ready",
  reason: hashMatchReason,
});

export const hashMatchTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: hashMatchDocument,
  events: [
    event({
      id: "evt-hash-verify",
      documentId: hashMatchDocument.id,
      stage: "verify",
      event: "completed",
      occurredAt: "2026-11-02T14:00:10Z",
      workflowId: "workflow-hash",
      runId: "run-hash",
    }),
    event({
      id: "evt-hash-cache",
      documentId: hashMatchDocument.id,
      stage: "cache_lookup",
      event: "completed",
      occurredAt: "2026-11-02T14:00:20Z",
      workflowId: "workflow-hash",
      runId: "run-hash",
      message: hashMatchReason,
      reasonCode: "hash_match",
      reasonMessage: hashMatchReason,
    }),
    event({
      id: "evt-hash-prepare",
      documentId: hashMatchDocument.id,
      stage: "prepare",
      event: "skipped",
      occurredAt: "2026-11-02T14:00:21Z",
      workflowId: "workflow-hash",
      runId: "run-hash",
      message: hashMatchReason,
    }),
    event({
      id: "evt-hash-extract",
      documentId: hashMatchDocument.id,
      stage: "extract",
      event: "skipped",
      occurredAt: "2026-11-02T14:00:22Z",
      workflowId: "workflow-hash",
      runId: "run-hash",
      message: hashMatchReason,
    }),
    event({
      id: "evt-hash-validate",
      documentId: hashMatchDocument.id,
      stage: "validate",
      event: "skipped",
      occurredAt: "2026-11-02T14:00:23Z",
      workflowId: "workflow-hash",
      runId: "run-hash",
      message: hashMatchReason,
    }),
    event({
      id: "evt-hash-assemble",
      documentId: hashMatchDocument.id,
      stage: "assemble",
      event: "skipped",
      occurredAt: "2026-11-02T14:00:24Z",
      workflowId: "workflow-hash",
      runId: "run-hash",
      message: hashMatchReason,
    }),
    event({
      id: "evt-hash-publish",
      documentId: hashMatchDocument.id,
      stage: "publish",
      event: "completed",
      occurredAt: "2026-11-02T14:00:40Z",
      workflowId: "workflow-hash",
      runId: "run-hash",
      message: hashMatchReason,
    }),
  ],
  attempts: [],
});

export const reviewTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: reviewDocument,
  events: completedPipeline(
    reviewDocument.id,
    "workflow-review",
    "run-review",
    "evt-review",
  ),
  attempts: [
    {
      id: "attempt-review-1",
      document_id: reviewDocument.id,
      target_id: "target-review",
      extractor: "docling",
      extractor_version: "2",
      attempt: 1,
      status: "succeeded",
      cost_micros: 400_000,
      started_at: "2026-11-02T10:02:00Z",
      completed_at: "2026-11-02T10:11:00Z",
    },
  ],
});

export const partialTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: partialDocument,
  events: completedPipeline(
    partialDocument.id,
    "workflow-partial",
    "run-partial",
    "evt-partial",
  ),
  attempts: [
    {
      id: "attempt-partial-1",
      document_id: partialDocument.id,
      target_id: "target-partial",
      extractor: "docling",
      extractor_version: "2",
      attempt: 1,
      status: "succeeded",
      cost_micros: 300_000,
      started_at: "2026-11-02T09:02:00Z",
      completed_at: "2026-11-02T09:10:00Z",
    },
  ],
});

export const queuedTimeline: DocumentTimeline = documentTimelineSchema.parse({
  document: queuedDocument,
  events: [],
  attempts: [],
});

export const timelinesById: Record<string, DocumentTimeline> = {
  [processingDocument.id]: processingTimeline,
  [readyDocument.id]: readyTimeline,
  [blockedDocument.id]: blockedTimeline,
  [reviewDocument.id]: reviewTimeline,
  [partialDocument.id]: partialTimeline,
  [queuedDocument.id]: queuedTimeline,
  [hashMatchDocument.id]: hashMatchTimeline,
  [automaticImagesDocument.id]: automaticImagesTimeline,
};

export const firstDocumentPage: DocumentList = documentListSchema.parse({
  documents: [processingDocument, readyDocument, blockedDocument],
  next_cursor: "page-2",
});

export const secondDocumentPage: DocumentList = documentListSchema.parse({
  documents: [readyDocument, reviewDocument, partialDocument, queuedDocument],
  next_cursor: null,
});

export const documentListFixture = firstDocumentPage;
export const documentTimelineFixture = processingTimeline;
