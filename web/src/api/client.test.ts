import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  InvalidApiResponseError,
  createApiClient,
  rewriteContentObjectURLs,
  rewriteLocalObjectURL,
} from "./client";
import { readyContent } from "../fixtures/content";
import { UploadExpiredError, UploadFailedError } from "../features/upload/errors";

afterEach(() => {
  vi.unstubAllGlobals();
});

const validDocument = {
  id: "doc-1",
  status: "processing",
  file_name: "report.pdf",
  size_bytes: 2048,
  created_at: "2026-11-02T13:00:00Z",
  updated_at: "2026-11-02T13:01:00Z",
};

const registration = {
  job: {
    ...validDocument,
    status: "awaiting_upload",
    updated_at: "2026-11-02T13:00:00Z",
  },
  upload: {
    url: "http://127.0.0.1:8080/uploads/staging/doc-1?exp=9999999999&ct=application%2Fpdf&max=1024&sig=abc",
    method: "PUT",
    headers: { "Content-Type": "application/pdf" },
    expires_at: "2099-01-01T00:00:00Z",
  },
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("API client", () => {
  it("returns validated success responses", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(validDocument),
    );
    const client = createApiClient({ baseUrl: "/api", fetch: fetcher });

    await expect(client.getDocument("doc-1")).resolves.toEqual(validDocument);
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/v1/documents/doc-1");
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("Accept")).toBe(
      "application/json",
    );
  });

  it("surfaces the API error contract", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse({ error: "document is not ready" }, 409));
    const client = createApiClient({ fetch: fetcher });

    await expect(client.getDocument("doc-1")).rejects.toMatchObject({
      name: "ApiError",
      status: 409,
      message: "document is not ready",
    });
  });

  it("preserves unauthorized status", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse({ error: "missing or invalid API key" }, 401));
    const client = createApiClient({ fetch: fetcher });

    const error = await client.getDocument("doc-1").catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 401 });
  });

  it("passes cancellation to fetch", async () => {
    const fetcher = vi.fn<typeof fetch>(
      (_input, init) =>
        new Promise((_resolve, reject) => {
          init?.signal?.addEventListener(
            "abort",
            () => {
              reject(new DOMException("The operation was aborted.", "AbortError"));
            },
            { once: true },
          );
        }),
    );
    const client = createApiClient({ fetch: fetcher });
    const controller = new AbortController();

    const request = client.getDocument("doc-1", controller.signal);
    controller.abort();

    await expect(request).rejects.toMatchObject({ name: "AbortError" });
  });

  it("rejects malformed success data", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse({ ...validDocument, size_bytes: "large" }));
    const client = createApiClient({ fetch: fetcher });

    await expect(client.getDocument("doc-1")).rejects.toBeInstanceOf(
      InvalidApiResponseError,
    );
  });

  it("fetches document content with the review query flag", async () => {
    const localContent = {
      ...readyContent,
      original: {
        ...readyContent.original,
        url: "http://127.0.0.1:8080/files/originals/doc-ready.pdf?exp=1&sig=o",
      },
    };
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(localContent));
    const client = createApiClient({ baseUrl: "/api", fetch: fetcher });

    await expect(
      client.getContent("doc-ready", { includeReview: true }),
    ).resolves.toMatchObject({
      original: { url: "/api/files/originals/doc-ready.pdf?exp=1&sig=o" },
      content: { version_id: readyContent.content.version_id },
    });
    expect(fetcher.mock.calls[0]?.[0]).toBe(
      "/api/v1/documents/doc-ready/content?include_review=true",
    );
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("Accept")).toBe(
      "application/json",
    );
  });

  it("registers, lists with a cursor, and submits documents", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(registration, 201))
      .mockResolvedValueOnce(
        jsonResponse({ documents: [validDocument], next_cursor: "next-1" }),
      )
      .mockResolvedValueOnce(
        jsonResponse({ ...validDocument, status: "queued" }, 202),
      )
      .mockResolvedValueOnce(
        jsonResponse({
          ...validDocument,
          status: "queued",
          extraction_mode: "pdfium",
        }, 202),
      );
    const client = createApiClient({ baseUrl: "/api", fetch: fetcher });

    await expect(
      client.register({
        file_name: "report.pdf",
        content_type: "application/pdf",
        size_bytes: 2048,
        extraction_mode: "docling",
      }),
    ).resolves.toEqual(registration);
    await expect(client.listDocuments("page-2")).resolves.toEqual({
      documents: [validDocument],
      next_cursor: "next-1",
    });
    await expect(client.submit("doc-1")).resolves.toMatchObject({ status: "queued" });
    await expect(
      client.submit("doc-1", undefined, { extraction_mode: "pdfium" }),
    ).resolves.toMatchObject({ status: "queued", extraction_mode: "pdfium" });

    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/v1/documents");
    expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: "POST" });
    const registerBody = fetcher.mock.calls[0]?.[1]?.body;
    expect(typeof registerBody).toBe("string");
    expect(JSON.parse(registerBody as string)).toMatchObject({
      extraction_mode: "docling",
    });
    expect(fetcher.mock.calls[1]?.[0]).toBe("/api/v1/documents?cursor=page-2");
    expect(fetcher.mock.calls[2]?.[0]).toBe("/api/v1/documents/doc-1/submit");
    expect(fetcher.mock.calls[2]?.[1]?.body).toBeUndefined();
    expect(fetcher.mock.calls[3]?.[0]).toBe("/api/v1/documents/doc-1/submit");
    const submitBody = fetcher.mock.calls[3]?.[1]?.body;
    expect(typeof submitBody).toBe("string");
    expect(JSON.parse(submitBody as string)).toEqual({
      extraction_mode: "pdfium",
    });
  });

  it("deletes a document and accepts 204", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 200 }))
      .mockResolvedValueOnce(jsonResponse({ error: "not found" }, 404));
    const client = createApiClient({ baseUrl: "/api", fetch: fetcher });

    await expect(client.deleteDocument("doc-1")).resolves.toBeUndefined();
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/v1/documents/doc-1");
    expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: "DELETE" });

    await expect(client.deleteDocument("doc-legacy-200")).rejects.toMatchObject({
      name: "ApiError",
      status: 200,
    });

    await expect(client.deleteDocument("missing")).rejects.toMatchObject({
      name: "ApiError",
      status: 404,
    });
  });

  it("rewrites local signed object URLs through the API proxy", () => {
    expect(
      rewriteLocalObjectURL(
        "http://localhost:8080/uploads/staging/a.pdf?exp=1&sig=x",
        "/api",
      ),
    ).toBe("/api/uploads/staging/a.pdf?exp=1&sig=x");
    expect(
      rewriteLocalObjectURL("https://storage.googleapis.com/bucket/obj?X-Goog=1", "/api"),
    ).toBe("https://storage.googleapis.com/bucket/obj?X-Goog=1");
    expect(
      rewriteLocalObjectURL("http://127.0.0.1:8080/files/derived/fig.png?exp=1&sig=y", "/api"),
    ).toBe("/api/files/derived/fig.png?exp=1&sig=y");
  });

  it("rewrites localblob content figure and original URLs", () => {
    const rewritten = rewriteContentObjectURLs(
      {
        ...readyContent,
        original: {
          ...readyContent.original,
          url: "http://127.0.0.1:8080/files/originals/doc-ready.pdf?exp=1&sig=o",
        },
        content: {
          ...readyContent.content,
          blocks: readyContent.content.blocks.map((block) =>
            block.kind === "figure"
              ? {
                  ...block,
                  asset_url: "http://127.0.0.1:8080/files/derived/leaf.png?exp=1&sig=a",
                  asset: {
                    url: "http://127.0.0.1:8080/files/derived/leaf.png?exp=1&sig=a",
                    content_type: "image/png",
                  },
                }
              : block,
          ),
        },
      },
      "/api",
    );
    expect(rewritten.original.url).toBe("/api/files/originals/doc-ready.pdf?exp=1&sig=o");
    const figure = rewritten.content.blocks.find((block) => block.kind === "figure");
    expect(figure?.asset_url).toBe("/api/files/derived/leaf.png?exp=1&sig=a");
    expect(figure?.asset?.url).toBe("/api/files/derived/leaf.png?exp=1&sig=a");
  });

  it("uploads through XHR with progress and maps expiry failures", async () => {
    class FakeXHR {
      static last: FakeXHR | undefined;
      status = 0;
      responseText = "";
      upload = { onprogress: null as ((event: ProgressEvent) => void) | null };
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      onabort: (() => void) | null = null;
      open = vi.fn();
      setRequestHeader = vi.fn();
      abort = vi.fn();
      send = vi.fn(() => {
        this.upload.onprogress?.({
          lengthComputable: true,
          loaded: 4,
          total: 8,
        } as ProgressEvent);
        this.status = 200;
        this.onload?.();
      });
      constructor() {
        FakeXHR.last = this;
      }
    }
    vi.stubGlobal("XMLHttpRequest", FakeXHR);
    const client = createApiClient({ fetch: vi.fn() });
    const loaded: number[] = [];
    await client.upload(
      registration.upload,
      new Blob(["%PDF-1.7"]),
      undefined,
      (next) => {
        loaded.push(next);
      },
    );
    expect(FakeXHR.last?.open).toHaveBeenCalledWith(
      "PUT",
      "/api/uploads/staging/doc-1?exp=9999999999&ct=application%2Fpdf&max=1024&sig=abc",
      true,
    );
    expect(loaded).toEqual([4, 8]);

    class ExpiredXHR extends FakeXHR {
      override send = vi.fn(() => {
        this.status = 403;
        this.responseText = "signed URL expired";
        this.onload?.();
      });
    }
    vi.stubGlobal("XMLHttpRequest", ExpiredXHR);
    await expect(
      client.upload(registration.upload, new Blob(["x"]), undefined, () => undefined),
    ).rejects.toBeInstanceOf(UploadExpiredError);

    await expect(
      client.upload(
        { ...registration.upload, expires_at: "2020-01-01T00:00:00Z" },
        new Blob(["x"]),
        undefined,
        () => undefined,
      ),
    ).rejects.toBeInstanceOf(UploadExpiredError);

    class FailedXHR extends FakeXHR {
      override send = vi.fn(() => {
        this.status = 500;
        this.onload?.();
      });
    }
    vi.stubGlobal("XMLHttpRequest", FailedXHR);
    await expect(
      client.upload(registration.upload, new Blob(["x"]), undefined, () => undefined),
    ).rejects.toBeInstanceOf(UploadFailedError);
  });
});
