import { z } from "zod";

const timestampSchema = z.iso.datetime({ offset: true });

export const PDF_CONTENT_TYPE = "application/pdf";
export const MAX_UPLOAD_BYTES = 100 * 1024 * 1024;
export const MAX_FILE_NAME_LENGTH = 255;
export const MAX_PROGRESS_UNIT_LENGTH = 32;
export const MAX_PROGRESS_REASON_LENGTH = 2048;

export const DOCUMENT_STATUSES = [
  "awaiting_upload",
  "queued",
  "processing",
  "ready",
  "partial",
  "needs_review",
  "blocked",
] as const;

export const FINAL_DOCUMENT_STATUSES = [
  "ready",
  "partial",
  "needs_review",
  "blocked",
] as const;

export const EXTRACTION_MODES = [
  "automatic",
  "pdfium",
  "docling",
  "gemini",
  "openai",
] as const;

export const PATH_DECISION_KINDS = ["chosen", "declined", "skipped"] as const;

/** Stable machine codes the UI may show for Automatic path transparency. */
export const PATH_REASON_CODES = [
  "chain_first",
  "pinned_start",
  "escalated",
  "figures_missing",
  "layout_missing",
  "validation_accept",
  "extractor_unset",
  "docling_required",
  "hash_match",
  "coverage_low",
  "extractor_error",
  "chain_exhausted",
] as const;

export const documentStatusSchema = z.enum(DOCUMENT_STATUSES);
export const extractionModeSchema = z.enum(EXTRACTION_MODES);
export const pathDecisionKindSchema = z.enum(PATH_DECISION_KINDS);

export const pathReasonSchema = z.object({
  reason_code: z.string().min(1).max(64),
  reason_message: z.string().min(1).max(MAX_PROGRESS_REASON_LENGTH),
});

export const pathDecisionSchema = pathReasonSchema.extend({
  kind: pathDecisionKindSchema,
  extractor: z.string().min(1).optional(),
  target_id: z.string().min(1).optional(),
  occurred_at: timestampSchema.optional(),
});

export const documentSchema = z.object({
  id: z.string().min(1),
  status: documentStatusSchema,
  file_name: z.string().min(1),
  size_bytes: z.number().int().nonnegative(),
  file_hash: z.string().optional(),
  version_id: z.string().optional(),
  reason: z.string().optional(),
  extraction_mode: extractionModeSchema.optional(),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const STAGE_NAMES = [
  "verify",
  "cache_lookup",
  "prepare",
  "extract",
  "validate",
  "assemble",
  "publish",
] as const;

export const STAGE_EVENT_TYPES = [
  "started",
  "progress",
  "completed",
  "failed",
  "skipped",
] as const;

export const stageNameSchema = z.enum(STAGE_NAMES);
export const stageEventTypeSchema = z.enum(STAGE_EVENT_TYPES);

export const progressMetadataSchema = z
  .object({
    current: z.number().int().nonnegative(),
    total: z.number().int().positive(),
    unit: z.string().min(1).max(MAX_PROGRESS_UNIT_LENGTH),
  })
  .refine((progress) => progress.current <= progress.total, {
    path: ["current"],
    message: "progress current must not exceed total",
  });

export const stageEventSchema = z
  .object({
    id: z.string().min(1),
    document_id: z.string().min(1),
    workflow_id: z.string().min(1),
    run_id: z.string().min(1),
    stage: stageNameSchema,
    event: stageEventTypeSchema,
    retry: z.number().int().nonnegative(),
    occurred_at: timestampSchema,
    progress: progressMetadataSchema.optional(),
    message: z.string().max(MAX_PROGRESS_REASON_LENGTH).optional(),
    reason_code: z.string().min(1).max(64).optional(),
    reason_message: z.string().min(1).max(MAX_PROGRESS_REASON_LENGTH).optional(),
  })
  .superRefine((event, ctx) => {
    if (event.event === "progress" && event.progress === undefined) {
      ctx.addIssue({
        code: "custom",
        path: ["progress"],
        message: "progress metadata is required for progress status",
      });
    }
  });

export const extractionAttemptSchema = z.object({
  id: z.string().min(1),
  document_id: z.string().min(1),
  target_id: z.string().min(1),
  extractor: z.string().min(1),
  extractor_version: z.string().min(1),
  attempt: z.number().int().positive(),
  status: z.enum(["started", "succeeded", "failed"]),
  cost_micros: z.number().int().nonnegative(),
  started_at: timestampSchema,
  completed_at: timestampSchema.optional(),
  error: z.string().optional(),
  reason_code: z.string().min(1).max(64).optional(),
  reason_message: z.string().min(1).max(MAX_PROGRESS_REASON_LENGTH).optional(),
  decision: pathDecisionKindSchema.optional(),
});

export const documentListSchema = z.object({
  documents: z.array(documentSchema),
  next_cursor: z.string().nullable(),
});

export const documentTimelineSchema = z.object({
  document: documentSchema,
  events: z.array(stageEventSchema),
  attempts: z.array(extractionAttemptSchema),
  decisions: z.array(pathDecisionSchema).optional(),
});

export const signedUploadSchema = z.object({
  url: z.url(),
  method: z.string().min(1),
  headers: z.record(z.string(), z.string()).optional(),
  expires_at: timestampSchema,
});

export const registrationSchema = z.object({
  job: documentSchema,
  upload: signedUploadSchema,
});

export const registerRequestSchema = z.object({
  file_name: z.string().min(1).max(MAX_FILE_NAME_LENGTH),
  content_type: z.literal(PDF_CONTENT_TYPE),
  size_bytes: z.number().int().positive().max(MAX_UPLOAD_BYTES),
  extraction_mode: extractionModeSchema.optional(),
});

/** Optional body for POST /v1/documents/{id}/submit (pin-then-escalate override). */
export const submitRequestSchema = z.object({
  extraction_mode: extractionModeSchema.optional(),
});

export const apiErrorSchema = z.object({
  error: z.string().min(1),
});

export const BLOCK_KINDS = [
  "paragraph",
  "heading",
  "table",
  "formula",
  "figure",
] as const;

export const TEXT_DIRECTIONS = ["ltr", "rtl"] as const;

export const PUBLISHED_CONTENT_STATUSES = [
  "ready",
  "partial",
  "needs_review",
] as const;

export const blockKindSchema = z.enum(BLOCK_KINDS);
export const textDirectionSchema = z.enum(TEXT_DIRECTIONS);
export const publishedContentStatusSchema = z.enum(PUBLISHED_CONTENT_STATUSES);

export const provenanceSchema = z.object({
  target_id: z.string().min(1),
  extractor: z.string().min(1),
  extractor_version: z.string().min(1),
  attempt_id: z.string().min(1),
});

export const bboxSchema = z.object({
  left: z.number(),
  top: z.number(),
  right: z.number(),
  bottom: z.number(),
});

export const contentAssetSchema = z.object({
  url: z.url(),
  content_type: z.string().min(1).optional(),
});

export const contentBlockSchema = z
  .object({
    kind: blockKindSchema,
    text: z.string().optional(),
    rows: z.array(z.array(z.string())).optional(),
    page: z.number().int().positive(),
    end_page: z.number().int().positive().optional(),
    direction: textDirectionSchema.optional(),
    bbox: bboxSchema.optional(),
    asset_url: z.url().optional(),
    asset: contentAssetSchema.optional(),
    provenance: provenanceSchema,
  })
  .superRefine((block, ctx) => {
    if (block.kind === "table") {
      if (!block.rows || block.rows.length === 0) {
        ctx.addIssue({
          code: "custom",
          path: ["rows"],
          message: "table blocks require at least one row",
        });
      }
      return;
    }
    if (block.kind === "figure") {
      const hasAsset = Boolean(block.asset_url ?? block.asset?.url);
      if (block.text === undefined && !hasAsset) {
        ctx.addIssue({
          code: "custom",
          path: ["text"],
          message: "figure blocks require text or an asset URL",
        });
      }
      return;
    }
    if (block.text === undefined) {
      ctx.addIssue({
        code: "custom",
        path: ["text"],
        message: `${block.kind} blocks require text`,
      });
    }
  });

export const reviewItemSchema = z.object({
  target_id: z.string().min(1),
  pages: z.array(z.number().int().positive()),
  reason: z.string().min(1),
  code: z.string().min(1).max(64).optional(),
  failed_checks: z.array(z.string().min(1)).optional(),
});

export const extractedContentSchema = z.object({
  version_id: z.string().min(1),
  job_id: z.string().min(1),
  status: publishedContentStatusSchema,
  coverage: z.number().min(0).max(1),
  pipeline_version: z.string().min(1),
  pages: z.number().int().nonnegative(),
  blocks: z.array(contentBlockSchema),
  review: z.array(reviewItemSchema).optional(),
  missing: z.array(reviewItemSchema).optional(),
  created_at: timestampSchema,
});

export const contentResponseSchema = z.object({
  content: extractedContentSchema,
  original: signedUploadSchema,
});

export type Document = z.infer<typeof documentSchema>;
export type DocumentList = z.infer<typeof documentListSchema>;
export type DocumentStatus = z.infer<typeof documentStatusSchema>;
export type DocumentTimeline = z.infer<typeof documentTimelineSchema>;
export type ExtractionAttempt = z.infer<typeof extractionAttemptSchema>;
export type ExtractionMode = z.infer<typeof extractionModeSchema>;
export type PathDecision = z.infer<typeof pathDecisionSchema>;
export type PathDecisionKind = z.infer<typeof pathDecisionKindSchema>;
export type RegisterRequest = z.infer<typeof registerRequestSchema>;
export type Registration = z.infer<typeof registrationSchema>;
export type SignedUpload = z.infer<typeof signedUploadSchema>;
export type SubmitRequest = z.infer<typeof submitRequestSchema>;
export type StageEvent = z.infer<typeof stageEventSchema>;
export type StageEventType = z.infer<typeof stageEventTypeSchema>;
export type StageName = z.infer<typeof stageNameSchema>;
export type BlockKind = z.infer<typeof blockKindSchema>;
export type TextDirection = z.infer<typeof textDirectionSchema>;
export type ContentAsset = z.infer<typeof contentAssetSchema>;
export type ContentBlock = z.infer<typeof contentBlockSchema>;
export type ExtractedContent = z.infer<typeof extractedContentSchema>;
export type ContentResponse = z.infer<typeof contentResponseSchema>;
export type ReviewItem = z.infer<typeof reviewItemSchema>;
export type PublishedContentStatus = z.infer<typeof publishedContentStatusSchema>;
export type Provenance = z.infer<typeof provenanceSchema>;

/** Prefer `asset.url`, then top-level `asset_url`. */
export function figureAssetURL(block: ContentBlock): string | null {
  if (block.kind !== "figure") return null;
  if (block.asset?.url) return block.asset.url;
  if (block.asset_url) return block.asset_url;
  return null;
}

const finalDocumentStatuses: ReadonlySet<string> = new Set(FINAL_DOCUMENT_STATUSES);
const publishedContentStatuses: ReadonlySet<string> = new Set(PUBLISHED_CONTENT_STATUSES);

export function isFinalDocumentStatus(status: DocumentStatus): boolean {
  return finalDocumentStatuses.has(status);
}

export function isPublishedDocumentStatus(status: DocumentStatus): boolean {
  return publishedContentStatuses.has(status);
}
