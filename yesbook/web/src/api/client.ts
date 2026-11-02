import type { z } from "zod";
import {
  apiErrorSchema,
  contentResponseSchema,
  documentListSchema,
  documentSchema,
  documentTimelineSchema,
  registrationSchema,
  type ContentBlock,
  type ContentResponse,
  type Document,
  type DocumentList,
  type DocumentTimeline,
  type RegisterRequest,
  type Registration,
  type SignedUpload,
  type SubmitRequest,
} from "./contracts";
import { UploadExpiredError, UploadFailedError } from "../features/upload/errors";

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export class InvalidApiResponseError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "InvalidApiResponseError";
  }
}

export interface ApiClient {
  health(signal?: AbortSignal): Promise<void>;
  getDocument(id: string, signal?: AbortSignal): Promise<Document>;
  listDocuments(cursor?: string | null, signal?: AbortSignal): Promise<DocumentList>;
  getTimeline(id: string, signal?: AbortSignal): Promise<DocumentTimeline>;
  getContent(
    id: string,
    options?: { includeReview?: boolean },
    signal?: AbortSignal,
  ): Promise<ContentResponse>;
  register(input: RegisterRequest, signal?: AbortSignal): Promise<Registration>;
  submit(
    documentId: string,
    signal?: AbortSignal,
    input?: SubmitRequest,
  ): Promise<Document>;
  deleteDocument(id: string, signal?: AbortSignal): Promise<void>;
  upload(
    target: SignedUpload,
    file: Blob,
    signal: AbortSignal | undefined,
    onProgress: (loaded: number, total: number) => void,
  ): Promise<void>;
}

interface ClientOptions {
  baseUrl?: string;
  fetch?: typeof globalThis.fetch;
  /** When set, rewrite local `/uploads/` and `/files/` signed URLs through this base. */
  objectProxyBase?: string | null;
}

async function readJson(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch (error) {
    throw new InvalidApiResponseError("The API returned malformed JSON.", {
      cause: error,
    });
  }
}

async function throwApiError(response: Response): Promise<never> {
  let message = `API request failed with status ${String(response.status)}.`;
  try {
    const body = await readJson(response);
    const parsedError = apiErrorSchema.safeParse(body);
    if (parsedError.success) message = parsedError.data.error;
  } catch {
    // Keep the status-based fallback when the body is empty or non-JSON.
  }
  throw new ApiError(response.status, message);
}

async function parseBody<T>(response: Response, schema: z.ZodType<T>): Promise<T> {
  if (!response.ok) {
    await throwApiError(response);
  }

  const parsed = schema.safeParse(await readJson(response));
  if (!parsed.success) {
    throw new InvalidApiResponseError("The API response did not match its contract.", {
      cause: parsed.error,
    });
  }
  return parsed.data;
}

async function request<T>(
  fetcher: typeof globalThis.fetch,
  url: string,
  schema: z.ZodType<T>,
  init?: RequestInit,
): Promise<T> {
  const headers = new Headers(init?.headers);
  if (!headers.has("Accept")) headers.set("Accept", "application/json");
  const response = await fetcher(url, {
    ...init,
    headers,
    signal: init?.signal ?? null,
  });
  return parseBody(response, schema);
}

function pathSegment(value: string): string {
  return encodeURIComponent(value);
}

/** Route local signed object URLs through the Vite/API proxy to avoid CORS. */
export function rewriteLocalObjectURL(url: string, proxyBase: string): string {
  let parsed: URL;
  try {
    parsed = new URL(url, "http://127.0.0.1");
  } catch {
    return url;
  }
  if (
    !parsed.pathname.startsWith("/uploads/") &&
    !parsed.pathname.startsWith("/files/")
  ) {
    return url;
  }
  const base = proxyBase.replace(/\/$/, "");
  return `${base}${parsed.pathname}${parsed.search}`;
}

function rewriteFigureBlock(block: ContentBlock, proxyBase: string): ContentBlock {
  if (block.kind !== "figure") return block;
  let next = block;
  if (block.asset_url) {
    next = { ...next, asset_url: rewriteLocalObjectURL(block.asset_url, proxyBase) };
  }
  if (block.asset?.url) {
    next = {
      ...next,
      asset: {
        ...block.asset,
        url: rewriteLocalObjectURL(block.asset.url, proxyBase),
      },
    };
  }
  return next;
}

/** Rewrite localblob `/files/` URLs on content + original to the API proxy base. */
export function rewriteContentObjectURLs(
  response: ContentResponse,
  proxyBase: string,
): ContentResponse {
  return {
    original: {
      ...response.original,
      url: rewriteLocalObjectURL(response.original.url, proxyBase),
    },
    content: {
      ...response.content,
      blocks: response.content.blocks.map((block) => rewriteFigureBlock(block, proxyBase)),
    },
  };
}

function putWithProgress(
  url: string,
  file: Blob,
  headers: Record<string, string> | undefined,
  signal: AbortSignal | undefined,
  onProgress: (loaded: number, total: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url, true);
    for (const [name, value] of Object.entries(headers ?? {})) {
      xhr.setRequestHeader(name, value);
    }
    xhr.upload.onprogress = (event) => {
      const total = event.lengthComputable ? event.total : file.size;
      onProgress(event.loaded, total > 0 ? total : file.size);
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        onProgress(file.size, file.size);
        resolve();
        return;
      }
      if (xhr.status === 403 && /expir/i.test(xhr.responseText)) {
        reject(new UploadExpiredError());
        return;
      }
      reject(new UploadFailedError());
    };
    xhr.onerror = () => {
      reject(new UploadFailedError());
    };
    xhr.onabort = () => {
      reject(new DOMException("The operation was aborted.", "AbortError"));
    };
    if (signal) {
      if (signal.aborted) {
        reject(new DOMException("The operation was aborted.", "AbortError"));
        return;
      }
      signal.addEventListener(
        "abort",
        () => {
          xhr.abort();
        },
        { once: true },
      );
    }
    xhr.send(file);
  });
}

export function createApiClient(options: ClientOptions = {}): ApiClient {
  const baseUrl = (options.baseUrl ?? "/api").replace(/\/$/, "");
  const objectProxyBase =
    options.objectProxyBase === undefined ? baseUrl : options.objectProxyBase;
  const fetcher =
    options.fetch ??
    ((input: RequestInfo | URL, init?: RequestInit) =>
      globalThis.fetch(input, init));

  return {
    async health(signal) {
      const response = await fetcher(`${baseUrl}/healthz`, {
        signal: signal ?? null,
      });
      if (!response.ok) {
        throw new ApiError(
          response.status,
          `Health check failed with status ${String(response.status)}.`,
        );
      }
    },
    getDocument(id, signal) {
      return request(
        fetcher,
        `${baseUrl}/v1/documents/${pathSegment(id)}`,
        documentSchema,
        { signal: signal ?? null },
      );
    },
    listDocuments(cursor, signal) {
      const params = new URLSearchParams();
      if (cursor) params.set("cursor", cursor);
      const query = params.toString();
      return request(
        fetcher,
        `${baseUrl}/v1/documents${query ? `?${query}` : ""}`,
        documentListSchema,
        { signal: signal ?? null },
      );
    },
    getTimeline(id, signal) {
      return request(
        fetcher,
        `${baseUrl}/v1/documents/${pathSegment(id)}/timeline`,
        documentTimelineSchema,
        { signal: signal ?? null },
      );
    },
    async getContent(id, options = {}, signal) {
      const params = new URLSearchParams();
      if (options.includeReview) params.set("include_review", "true");
      const query = params.toString();
      const payload = await request(
        fetcher,
        `${baseUrl}/v1/documents/${pathSegment(id)}/content${query ? `?${query}` : ""}`,
        contentResponseSchema,
        { signal: signal ?? null },
      );
      if (objectProxyBase === null) return payload;
      return rewriteContentObjectURLs(payload, objectProxyBase);
    },
    register(input, signal) {
      return request(fetcher, `${baseUrl}/v1/documents`, registrationSchema, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
        signal: signal ?? null,
      });
    },
    submit(documentId, signal, input) {
      const body =
        input?.extraction_mode === undefined
          ? undefined
          : JSON.stringify({ extraction_mode: input.extraction_mode });
      return request(
        fetcher,
        `${baseUrl}/v1/documents/${pathSegment(documentId)}/submit`,
        documentSchema,
        {
          method: "POST",
          ...(body
            ? { headers: { "Content-Type": "application/json" }, body }
            : {}),
          signal: signal ?? null,
        },
      );
    },
    async deleteDocument(id, signal) {
      const response = await fetcher(`${baseUrl}/v1/documents/${pathSegment(id)}`, {
        method: "DELETE",
        headers: { Accept: "application/json" },
        signal: signal ?? null,
      });
      // Backend returns 204 No Content with an empty body.
      if (response.status === 204) return;
      await throwApiError(response);
    },
    async upload(target, file, signal, onProgress) {
      if (Date.parse(target.expires_at) <= Date.now()) {
        throw new UploadExpiredError();
      }
      const url =
        objectProxyBase === null
          ? target.url
          : rewriteLocalObjectURL(target.url, objectProxyBase);
      await putWithProgress(url, file, target.headers, signal, onProgress);
    },
  };
}

export const apiClient = createApiClient();
