import {
  isFinalDocumentStatus,
  type Document,
  type DocumentList,
  type DocumentStatus,
  type DocumentTimeline,
  type ExtractionAttempt,
  type ExtractionMode,
  type PathDecision,
  type PathDecisionKind,
  type StageEvent,
  type StageEventType,
  type StageName,
} from "../../api/contracts";
import { describeFailure } from "../../app/messages";

/** Diagram pipeline stages shown in the UI (processing view). */
export const PIPELINE_STEP_IDS = [
  "verify",
  "prepare",
  "route",
  "extract",
  "validate",
  "assemble",
  "outcome",
] as const;

export type PipelineStepId = (typeof PIPELINE_STEP_IDS)[number];

export type ExtractorPath = "pdfium" | "docling" | "managed" | "other";

export interface ExtractorPathView {
  name: string;
  version: string;
  path: ExtractorPath;
  pathLabel: string;
  status: ExtractionAttempt["status"];
  attempt: number;
  decision: PathDecisionKind | null;
  reasonCode: string | null;
  reasonMessage: string | null;
}

export interface PathDecisionView {
  kind: PathDecisionKind;
  kindLabel: string;
  extractor: string | null;
  pathLabel: string | null;
  reasonCode: string;
  reasonMessage: string;
  targetId: string | null;
  occurredAt: string | null;
}

export type PipelineStepState =
  | "pending"
  | StageEventType
  | "shortcut"
  | "outcome_ready"
  | "outcome_partial"
  | "outcome_review"
  | "outcome_blocked";

export interface PipelineStepView {
  id: PipelineStepId;
  label: string;
  state: PipelineStepState;
  statusLabel: string;
  current: boolean;
  retry: number;
  message: string | null;
  reasonCode: string | null;
  progress: { current: number; total: number; unit: string } | null;
  progressLabel: string | null;
  occurredAt: string | null;
  workflowId: string | null;
  runId: string | null;
  extractors: ExtractorPathView[];
}

export interface PipelineView {
  steps: PipelineStepView[];
  hashMatch: boolean;
  hashMatchMessage: string | null;
  retryLoop: boolean;
  maxRetry: number;
  evidenceNote: string;
  outcome: DocumentStatus | null;
}

const PIPELINE_LABELS: Record<PipelineStepId, string> = {
  verify: "Verify",
  prepare: "Prepare",
  route: "Route",
  extract: "Extract",
  validate: "Validate",
  assemble: "Assemble",
  outcome: "Outcome",
};

const DOCUMENT_STATUS_LABELS: Record<DocumentStatus, string> = {
  awaiting_upload: "Awaiting upload",
  queued: "Queued",
  processing: "Processing",
  ready: "Ready",
  partial: "Partial",
  needs_review: "Needs review",
  blocked: "Blocked",
};

const EXTRACTION_MODE_LABELS: Record<ExtractionMode, string> = {
  automatic: "Automatic",
  pdfium: "Pinned: PDFium",
  docling: "Pinned: Docling",
  gemini: "Pinned: Gemini",
  openai: "Pinned: OpenAI",
};

const PATH_DECISION_LABELS: Record<PathDecisionKind, string> = {
  chosen: "Chosen",
  declined: "Declined",
  skipped: "Skipped",
};

const STEP_STATUS_LABELS: Record<PipelineStepState, string> = {
  pending: "Pending",
  started: "Started",
  progress: "In progress",
  completed: "Completed",
  failed: "Failed",
  skipped: "Skipped",
  shortcut: "Hash match",
  outcome_ready: "Ready",
  outcome_partial: "Partial",
  outcome_review: "Needs review",
  outcome_blocked: "Blocked",
};

const HASH_MATCH_REASON = "identical file already processed";
const HASH_MATCH_CODE = "hash_match";
const EVIDENCE_NOTE =
  "Each stage writes evidence with page anchors and provenance. Retries resume from saved output; published blocks reference their source page.";

const MANAGED_EXTRACTORS = new Set([
  "gemini",
  "openai",
  "document_ai",
  "documentai",
  "vertex",
  "vertex_ai",
  "managed",
]);

export function statusLabel(status: DocumentStatus): string {
  return DOCUMENT_STATUS_LABELS[status];
}

export function extractionModeLabel(mode: ExtractionMode | undefined): string {
  if (!mode) return EXTRACTION_MODE_LABELS.automatic;
  return EXTRACTION_MODE_LABELS[mode];
}

export function pathDecisionKindLabel(kind: PathDecisionKind): string {
  return PATH_DECISION_LABELS[kind];
}

export function extractorPathFor(name: string): ExtractorPath {
  const key = name.trim().toLowerCase();
  if (key === "pdfium") return "pdfium";
  if (key === "docling") return "docling";
  if (MANAGED_EXTRACTORS.has(key) || key.includes("gemini") || key.includes("openai")) {
    return "managed";
  }
  return "other";
}

export function extractorPathLabel(path: ExtractorPath): string {
  switch (path) {
    case "pdfium":
      return "PDFium";
    case "docling":
      return "Docling";
    case "managed":
      return "Managed";
    case "other":
      return "Other";
  }
}

function latestByStage(events: readonly StageEvent[]): Map<StageName, StageEvent> {
  const latest = new Map<StageName, StageEvent>();
  for (const event of events) latest.set(event.stage, event);
  return latest;
}

function eventIsHashMatch(event: StageEvent | undefined): boolean {
  if (!event) return false;
  if (event.reason_code === HASH_MATCH_CODE) return true;
  return event.message === HASH_MATCH_REASON || event.reason_message === HASH_MATCH_REASON;
}

function isHashMatch(events: readonly StageEvent[]): boolean {
  const latest = latestByStage(events);
  const cache = latest.get("cache_lookup");
  if (cache?.event === "completed" && eventIsHashMatch(cache)) return true;
  for (const stage of ["prepare", "extract", "validate", "assemble"] as const) {
    const event = latest.get(stage);
    if (event?.event === "skipped" && eventIsHashMatch(event)) return true;
  }
  return false;
}

function fromWireEvent(
  event: StageEvent | undefined,
): Omit<
  PipelineStepView,
  "id" | "label" | "current" | "extractors" | "state" | "statusLabel"
> & { state: "pending" | StageEventType } {
  const state: "pending" | StageEventType = event?.event ?? "pending";
  const progress = state === "progress" ? (event?.progress ?? null) : null;
  return {
    state,
    retry: event?.retry ?? 0,
    message: event?.reason_message ?? event?.message ?? null,
    reasonCode: event?.reason_code ?? null,
    progress,
    progressLabel: progress
      ? `${String(progress.current)} of ${String(progress.total)} ${progress.unit}`
      : null,
    occurredAt: event?.occurred_at ?? null,
    workflowId: event?.workflow_id ?? null,
    runId: event?.run_id ?? null,
  };
}

function attemptViews(attempts: readonly ExtractionAttempt[]): ExtractorPathView[] {
  return [...attempts]
    .sort((a, b) => a.attempt - b.attempt || a.started_at.localeCompare(b.started_at))
    .map((attempt) => {
      const path = extractorPathFor(attempt.extractor);
      return {
        name: attempt.extractor,
        version: attempt.extractor_version,
        path,
        pathLabel: extractorPathLabel(path),
        status: attempt.status,
        attempt: attempt.attempt,
        decision: attempt.decision ?? null,
        reasonCode: attempt.reason_code ?? null,
        reasonMessage: attempt.reason_message ?? null,
      };
    });
}

function decisionFromAttempt(attempt: ExtractionAttempt): PathDecisionView | null {
  if (!attempt.reason_code || !attempt.reason_message) return null;
  const kind = attempt.decision ?? (attempt.status === "succeeded" ? "chosen" : "declined");
  const path = extractorPathFor(attempt.extractor);
  return {
    kind,
    kindLabel: pathDecisionKindLabel(kind),
    extractor: attempt.extractor,
    pathLabel: extractorPathLabel(path),
    reasonCode: attempt.reason_code,
    reasonMessage: attempt.reason_message,
    targetId: attempt.target_id,
    occurredAt: attempt.completed_at ?? attempt.started_at,
  };
}

function decisionFromWire(decision: PathDecision): PathDecisionView {
  const path = decision.extractor ? extractorPathFor(decision.extractor) : null;
  return {
    kind: decision.kind,
    kindLabel: pathDecisionKindLabel(decision.kind),
    extractor: decision.extractor ?? null,
    pathLabel: path ? extractorPathLabel(path) : null,
    reasonCode: decision.reason_code,
    reasonMessage: decision.reason_message,
    targetId: decision.target_id ?? null,
    occurredAt: decision.occurred_at ?? null,
  };
}

/** Prefer explicit timeline.decisions; fall back to attempt reason fields. */
export function collectPathDecisions(timeline: DocumentTimeline): PathDecisionView[] {
  if (timeline.decisions && timeline.decisions.length > 0) {
    return timeline.decisions.map(decisionFromWire);
  }
  return timeline.attempts
    .map(decisionFromAttempt)
    .filter((item): item is PathDecisionView => item !== null);
}

function routeSummary(extractors: readonly ExtractorPathView[]): string {
  if (extractors.length === 0) return "Selecting extractor by content type, language, and precision.";
  const unique = new Map<string, ExtractorPathView>();
  for (const item of extractors) {
    const key = `${item.path}:${item.name}:${item.version}`;
    if (!unique.has(key)) unique.set(key, item);
  }
  const labels = [...unique.values()].map(
    (item) => `${item.pathLabel} (${item.name} ${item.version})`,
  );
  return `Extractor versions pinned: ${labels.join(" · ")}.`;
}

function outcomeState(status: DocumentStatus): PipelineStepState {
  switch (status) {
    case "ready":
      return "outcome_ready";
    case "partial":
      return "outcome_partial";
    case "needs_review":
      return "outcome_review";
    case "blocked":
      return "outcome_blocked";
    default:
      return "pending";
  }
}

function markCurrent(steps: PipelineStepView[]): void {
  let currentIndex = -1;
  steps.forEach((step, index) => {
    if (step.id === "outcome") return;
    if (step.state === "started" || step.state === "progress" || step.state === "failed") {
      currentIndex = index;
    }
  });
  if (currentIndex < 0) {
    const outcomeIndex = steps.findIndex((step) => step.id === "outcome");
    const outcome = outcomeIndex >= 0 ? steps[outcomeIndex] : undefined;
    if (
      outcome &&
      (outcome.state === "started" ||
        outcome.state === "progress" ||
        outcome.state === "failed" ||
        outcome.state === "outcome_blocked")
    ) {
      currentIndex = outcomeIndex;
    } else {
      const firstPending = steps.findIndex((step) => step.state === "pending");
      const allSettled = steps.every(
        (step) =>
          step.state === "completed" ||
          step.state === "skipped" ||
          step.state === "shortcut" ||
          step.state.startsWith("outcome_"),
      );
      if (!allSettled && firstPending >= 0) currentIndex = firstPending;
    }
  }
  if (currentIndex >= 0) {
    const current = steps[currentIndex];
    if (current) current.current = true;
  }
}

/**
 * Maps wire progress events + attempts onto the processing-view pipeline:
 * Verify → Prepare → Route → Extract → Validate → Assemble → Outcome.
 * `cache_lookup` becomes a hash-match shortcut; `publish` folds into Outcome.
 */
export function derivePipelineView(timeline: DocumentTimeline): PipelineView {
  const { document, events, attempts } = timeline;
  const latest = latestByStage(events);
  const hashMatch = isHashMatch(events);
  const extractors = attemptViews(attempts);
  const maxRetry = Math.max(0, ...events.map((event) => event.retry), ...attempts.map((a) => a.attempt - 1));
  const retryLoop = maxRetry > 0 || attempts.some((attempt) => attempt.attempt > 1);

  const verify = fromWireEvent(latest.get("verify"));
  const prepare = fromWireEvent(latest.get("prepare"));
  const extract = fromWireEvent(latest.get("extract"));
  const validate = fromWireEvent(latest.get("validate"));
  const assemble = fromWireEvent(latest.get("assemble"));
  const publish = latest.get("publish");
  const cache = latest.get("cache_lookup");

  let routeState: "pending" | StageEventType = "pending";
  let routeMessage: string | null = null;
  let routeReasonCode: string | null = null;
  let routeOccurredAt: string | null = null;
  let routeWorkflowId: string | null = null;
  let routeRunId: string | null = null;

  if (hashMatch) {
    routeState = "skipped";
    routeMessage = cache?.reason_message ?? cache?.message ?? HASH_MATCH_REASON;
    routeReasonCode = cache?.reason_code ?? HASH_MATCH_CODE;
    routeOccurredAt = cache?.occurred_at ?? prepare.occurredAt;
    routeWorkflowId = cache?.workflow_id ?? prepare.workflowId;
    routeRunId = cache?.run_id ?? prepare.runId;
  } else if (prepare.state === "failed") {
    routeState = "pending";
  } else if (prepare.state === "skipped") {
    routeState = "skipped";
    routeMessage = prepare.message;
    routeReasonCode = prepare.reasonCode;
  } else if (extractors.length > 0 || extract.state !== "pending") {
    routeState = "completed";
    routeMessage = routeSummary(extractors);
    const firstAttempt = attempts[0];
    routeOccurredAt = firstAttempt?.started_at ?? extract.occurredAt ?? prepare.occurredAt;
    routeWorkflowId = extract.workflowId ?? prepare.workflowId;
    routeRunId = extract.runId ?? prepare.runId;
  } else if (prepare.state === "completed") {
    routeState = "started";
    routeMessage = routeSummary(extractors);
    routeOccurredAt = prepare.occurredAt;
    routeWorkflowId = prepare.workflowId;
    routeRunId = prepare.runId;
  } else if (prepare.state === "started" || prepare.state === "progress") {
    routeState = "pending";
  }

  let extractMessage = extract.message;
  if (!extractMessage && extractors.length > 0) {
    const paths = [...new Set(extractors.map((item) => item.pathLabel))];
    extractMessage = `Extractor path: ${paths.join(" · ")}.`;
  }

  let outcomeStep: PipelineStepView;
  if (isFinalDocumentStatus(document.status)) {
    const state = outcomeState(document.status);
    outcomeStep = {
      id: "outcome",
      label: PIPELINE_LABELS.outcome,
      state,
      statusLabel: STEP_STATUS_LABELS[state],
      current: false,
      retry: publish?.retry ?? assemble.retry,
      message: document.reason ?? publish?.reason_message ?? publish?.message ?? assemble.message,
      reasonCode: publish?.reason_code ?? assemble.reasonCode,
      progress: null,
      progressLabel: null,
      occurredAt: document.updated_at,
      workflowId: publish?.workflow_id ?? assemble.workflowId,
      runId: publish?.run_id ?? assemble.runId,
      extractors: [],
    };
  } else if (publish) {
    const base = fromWireEvent(publish);
    outcomeStep = {
      id: "outcome",
      label: PIPELINE_LABELS.outcome,
      ...base,
      statusLabel: STEP_STATUS_LABELS[base.state],
      current: false,
      extractors: [],
    };
  } else {
    outcomeStep = {
      id: "outcome",
      label: PIPELINE_LABELS.outcome,
      state: "pending",
      statusLabel: STEP_STATUS_LABELS.pending,
      current: false,
      retry: 0,
      message: null,
      reasonCode: null,
      progress: null,
      progressLabel: null,
      occurredAt: null,
      workflowId: null,
      runId: null,
      extractors: [],
    };
  }

  const applyHashShortcut = <
    T extends {
      state: PipelineStepState;
      message: string | null;
      reasonCode: string | null;
      statusLabel: string;
    },
  >(
    step: T,
  ): T => {
    if (!hashMatch) return step;
    if (step.state === "pending" || step.state === "started" || step.state === "progress") {
      return {
        ...step,
        state: "skipped",
        statusLabel: STEP_STATUS_LABELS.skipped,
        message: step.message ?? HASH_MATCH_REASON,
        reasonCode: step.reasonCode ?? HASH_MATCH_CODE,
      };
    }
    return step;
  };

  const steps: PipelineStepView[] = [
    {
      id: "verify",
      label: PIPELINE_LABELS.verify,
      ...verify,
      statusLabel: STEP_STATUS_LABELS[verify.state],
      current: false,
      extractors: [],
    },
    applyHashShortcut({
      id: "prepare",
      label: PIPELINE_LABELS.prepare,
      ...prepare,
      statusLabel: STEP_STATUS_LABELS[prepare.state],
      current: false,
      extractors: [],
    }),
    applyHashShortcut({
      id: "route",
      label: PIPELINE_LABELS.route,
      state: routeState,
      statusLabel: STEP_STATUS_LABELS[routeState],
      current: false,
      retry: 0,
      message: routeMessage,
      reasonCode: routeReasonCode,
      progress: null,
      progressLabel: null,
      occurredAt: routeOccurredAt,
      workflowId: routeWorkflowId,
      runId: routeRunId,
      extractors,
    }),
    applyHashShortcut({
      id: "extract",
      label: PIPELINE_LABELS.extract,
      ...extract,
      message: extractMessage,
      statusLabel: STEP_STATUS_LABELS[extract.state],
      current: false,
      extractors,
    }),
    applyHashShortcut({
      id: "validate",
      label: PIPELINE_LABELS.validate,
      ...validate,
      statusLabel: STEP_STATUS_LABELS[validate.state],
      current: false,
      extractors: [],
    }),
    applyHashShortcut({
      id: "assemble",
      label: PIPELINE_LABELS.assemble,
      ...assemble,
      statusLabel: STEP_STATUS_LABELS[assemble.state],
      current: false,
      extractors: [],
    }),
    hashMatch && !isFinalDocumentStatus(document.status)
      ? {
          ...outcomeStep,
          state: "shortcut",
          statusLabel: STEP_STATUS_LABELS.shortcut,
          message: HASH_MATCH_REASON,
          reasonCode: HASH_MATCH_CODE,
        }
      : outcomeStep,
  ];

  markCurrent(steps);

  return {
    steps,
    hashMatch,
    hashMatchMessage: hashMatch ? "Hash match: return the existing version." : null,
    retryLoop,
    maxRetry,
    evidenceNote: EVIDENCE_NOTE,
    outcome: isFinalDocumentStatus(document.status) ? document.status : null,
  };
}

export function timelineSummary(timeline: DocumentTimeline): string {
  const label = statusLabel(timeline.document.status);
  const name = timeline.document.file_name;
  if (isFinalDocumentStatus(timeline.document.status)) {
    const reason = timeline.document.reason;
    return reason ? `${name} is ${label}. ${reason}` : `${name} is ${label}.`;
  }
  const view = derivePipelineView(timeline);
  if (view.hashMatch) {
    return `${name} is ${label}. ${view.hashMatchMessage ?? "Hash match."}`;
  }
  const active = view.steps.find((step) => step.current);
  if (active?.progressLabel) {
    return `${name} is ${label}. ${active.label}: ${active.progressLabel}.`;
  }
  if (active) {
    return `${name} is ${label}. ${active.label} ${active.statusLabel.toLowerCase()}.`;
  }
  return `${name} is ${label}.`;
}

export function mergeDocuments(pages: readonly DocumentList[]): Document[] {
  const seen = new Set<string>();
  const documents: Document[] = [];
  for (const page of pages) {
    for (const document of page.documents) {
      if (seen.has(document.id)) continue;
      seen.add(document.id);
      documents.push(document);
    }
  }
  return documents;
}

export type DocumentListUiState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "empty" }
  | {
      status: "ready";
      documents: Document[];
      hasMore: boolean;
      loadingMore: boolean;
      moreError: string | null;
    };

export function toDocumentListState(input: {
  pages: readonly DocumentList[] | undefined;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  initialError: unknown;
  nextPageError: unknown;
}): DocumentListUiState {
  if (!input.pages) {
    if (input.initialError) {
      return {
        status: "error",
        message: describeFailure(
          input.initialError,
          "The document list could not be loaded. Retry.",
        ),
      };
    }
    return { status: "loading" };
  }

  const documents = mergeDocuments(input.pages);
  if (documents.length === 0) return { status: "empty" };
  return {
    status: "ready",
    documents,
    hasMore: input.hasNextPage,
    loadingMore: input.isFetchingNextPage,
    moreError: input.nextPageError
      ? describeFailure(input.nextPageError, "The next page could not be loaded. Retry.")
      : null,
  };
}

export function attemptStatusLabel(
  status: DocumentTimeline["attempts"][number]["status"],
): string {
  switch (status) {
    case "started":
      return "Started";
    case "succeeded":
      return "Succeeded";
    case "failed":
      return "Failed";
  }
}
