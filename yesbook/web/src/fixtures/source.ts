import {
  PDF_CONTENT_TYPE,
  STAGE_NAMES,
  documentTimelineSchema,
  registrationSchema,
  type ContentResponse,
  type Document,
  type DocumentList,
  type DocumentTimeline,
  type RegisterRequest,
  type Registration,
  type SignedUpload,
  type SubmitRequest,
} from "../api/contracts";
import { ApiError } from "../api/client";
import { abortError, throwIfAborted } from "../app/abort";
import { UploadExpiredError } from "../features/upload/errors";
import { contentsByDocumentId } from "./content";
import { firstDocumentPage, secondDocumentPage, timelinesById } from "./documents";

export interface ContentQueryOptions {
  includeReview?: boolean;
}

export interface DocumentSource {
  listDocuments(cursor: string | null, signal: AbortSignal): Promise<DocumentList>;
  getTimeline(id: string, signal: AbortSignal): Promise<DocumentTimeline>;
  getContent(
    id: string,
    options: ContentQueryOptions,
    signal: AbortSignal,
  ): Promise<ContentResponse>;
  deleteDocument(id: string, signal: AbortSignal): Promise<void>;
}

export interface UploadSource {
  register(input: RegisterRequest, signal: AbortSignal): Promise<Registration>;
  upload(
    target: SignedUpload,
    file: Blob,
    signal: AbortSignal,
    onProgress: (loaded: number, total: number) => void,
  ): Promise<void>;
  submit(
    documentId: string,
    signal: AbortSignal,
    input?: SubmitRequest,
  ): Promise<Document>;
}

interface TimelineScript {
  frames: DocumentTimeline[];
  index: number;
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
  throwIfAborted(signal);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(abortError());
    };
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

function uploadFrames(document: Document): DocumentTimeline[] {
  const occurredAt = document.created_at;
  const identity = {
    document_id: document.id,
    workflow_id: `workflow-${document.id}`,
    run_id: `run-${document.id}`,
    retry: 0,
    occurred_at: occurredAt,
  };
  const queued = documentTimelineSchema.parse({
    document,
    events: [],
    attempts: [],
  });
  const processing = documentTimelineSchema.parse({
    document: { ...document, status: "processing" },
    events: [
      { ...identity, id: `${document.id}-verify`, stage: "verify", event: "completed" },
      {
        ...identity,
        id: `${document.id}-extract`,
        stage: "extract",
        event: "progress",
        progress: { current: 1, total: 2, unit: "pages" },
      },
    ],
    attempts: [
      {
        id: `${document.id}-attempt`,
        document_id: document.id,
        target_id: `${document.id}-target`,
        extractor: "docling",
        extractor_version: "2",
        attempt: 1,
        status: "started",
        cost_micros: 0,
        started_at: occurredAt,
      },
    ],
  });
  const ready = documentTimelineSchema.parse({
    document: { ...document, status: "ready", version_id: `ver-${document.id}` },
    events: STAGE_NAMES.map((stage) => ({
      ...identity,
      id: `${document.id}-${stage}`,
      stage,
      event: "completed" as const,
    })),
    attempts: [
      {
        id: `${document.id}-attempt-done`,
        document_id: document.id,
        target_id: `${document.id}-target`,
        extractor: "docling",
        extractor_version: "2",
        attempt: 1,
        status: "succeeded",
        cost_micros: 10_000,
        started_at: occurredAt,
        completed_at: occurredAt,
      },
    ],
  });
  return [queued, processing, ready];
}

export interface MemorySources {
  documents: DocumentSource;
  uploads: UploadSource;
}

export function createMemorySources(): MemorySources {
  let uploadCount = 0;
  const pending = new Map<string, Document>();
  const scripts = new Map<string, TimelineScript>();
  const uploaded: Document[] = [];
  const deleted = new Set<string>();

  return {
    documents: {
      listDocuments(cursor, signal) {
        throwIfAborted(signal);
        if (cursor === null) {
          return Promise.resolve({
            documents: [...firstDocumentPage.documents, ...uploaded].filter(
              (document) => !deleted.has(document.id),
            ),
            next_cursor: firstDocumentPage.next_cursor,
          });
        }
        if (cursor === "page-2") {
          return Promise.resolve({
            documents: secondDocumentPage.documents.filter(
              (document) => !deleted.has(document.id),
            ),
            next_cursor: null,
          });
        }
        return Promise.reject(new ApiError(400, "unknown document cursor"));
      },
      getTimeline(id, signal) {
        throwIfAborted(signal);
        if (deleted.has(id)) return Promise.reject(new ApiError(404, "not found"));
        const script = scripts.get(id);
        if (script) {
          const frame = script.frames[Math.min(script.index, script.frames.length - 1)];
          if (!frame) return Promise.reject(new ApiError(404, "not found"));
          if (script.index < script.frames.length - 1) script.index += 1;
          return Promise.resolve(structuredClone(frame));
        }
        const timeline = timelinesById[id];
        if (!timeline) return Promise.reject(new ApiError(404, "not found"));
        return Promise.resolve(structuredClone(timeline));
      },
      getContent(id, options, signal) {
        throwIfAborted(signal);
        if (deleted.has(id)) return Promise.reject(new ApiError(404, "not found"));
        const payload = contentsByDocumentId[id];
        if (!payload) {
          return Promise.reject(
            new ApiError(409, "document has no published content: status is processing"),
          );
        }
        if (payload.content.status === "needs_review" && !options.includeReview) {
          return Promise.reject(
            new ApiError(409, "content needs review and was not requested"),
          );
        }
        return Promise.resolve(structuredClone(payload));
      },
      deleteDocument(id, signal) {
        throwIfAborted(signal);
        const known =
          Boolean(timelinesById[id]) ||
          uploaded.some((document) => document.id === id) ||
          pending.has(id) ||
          Boolean(contentsByDocumentId[id]);
        if (!known || deleted.has(id)) {
          return Promise.reject(new ApiError(404, "not found"));
        }
        deleted.add(id);
        pending.delete(id);
        scripts.delete(id);
        const index = uploaded.findIndex((document) => document.id === id);
        if (index >= 0) uploaded.splice(index, 1);
        return Promise.resolve();
      },
    },
    uploads: {
      register(input, signal) {
        throwIfAborted(signal);
        uploadCount += 1;
        const now = new Date().toISOString();
        const registration = registrationSchema.parse({
          job: {
            id: `doc-upload-${String(uploadCount)}`,
            status: "awaiting_upload",
            file_name: input.file_name,
            size_bytes: input.size_bytes,
            created_at: now,
            updated_at: now,
            ...(input.extraction_mode ? { extraction_mode: input.extraction_mode } : {}),
          },
          upload: {
            url: `https://uploads.example.test/doc-upload-${String(uploadCount)}`,
            method: "PUT",
            headers: { "Content-Type": PDF_CONTENT_TYPE },
            expires_at: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
          },
        });
        pending.set(registration.job.id, registration.job);
        return Promise.resolve(registration);
      },
      async upload(target, file, signal, onProgress) {
        throwIfAborted(signal);
        if (Date.parse(target.expires_at) <= Date.now()) throw new UploadExpiredError();
        const total = file.size;
        const steps = 4;
        for (let step = 1; step <= steps; step += 1) {
          await delay(15, signal);
          onProgress(Math.round((total * step) / steps), total);
        }
      },
      submit(documentId, signal, input) {
        throwIfAborted(signal);
        const already = uploaded.find((item) => item.id === documentId);
        if (already) return Promise.resolve(already);
        const job = pending.get(documentId);
        if (!job) return Promise.reject(new ApiError(404, "not found"));
        const now = new Date().toISOString();
        const queued: Document = {
          ...job,
          status: "queued",
          updated_at: now,
          ...(input?.extraction_mode ? { extraction_mode: input.extraction_mode } : {}),
        };
        uploaded.push(queued);
        scripts.set(documentId, { frames: uploadFrames(queued), index: 0 });
        pending.delete(documentId);
        return Promise.resolve(queued);
      },
    },
  };
}
