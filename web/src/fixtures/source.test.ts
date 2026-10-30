import { describe, expect, it } from "vitest";
import type { ApiError } from "../api/client";
import { isFinalDocumentStatus } from "../api/contracts";
import { UploadExpiredError } from "../features/upload/errors";
import { createMemorySources } from "./source";

const signal = new AbortController().signal;

describe("memory sources", () => {
  it("simulates register, upload, and submit without a network call", async () => {
    const sources = createMemorySources();
    const file = new File(["%PDF-1.7"], "lesson.pdf", { type: "application/pdf" });
    const registration = await sources.uploads.register(
      {
        file_name: "lesson.pdf",
        content_type: "application/pdf",
        size_bytes: file.size,
      },
      signal,
    );
    let loaded = 0;
    await sources.uploads.upload(registration.upload, file, signal, (next) => {
      loaded = next;
    });
    expect(loaded).toBe(file.size);

    const submitted = await sources.uploads.submit(registration.job.id, signal);
    expect(submitted).toMatchObject({ status: "queued", file_name: "lesson.pdf" });
    const listed = await sources.documents.listDocuments(null, signal);
    expect(listed.documents.some((document) => document.id === submitted.id)).toBe(true);

    const queued = await sources.documents.getTimeline(submitted.id, signal);
    const processing = await sources.documents.getTimeline(submitted.id, signal);
    const ready = await sources.documents.getTimeline(submitted.id, signal);
    const again = await sources.documents.getTimeline(submitted.id, signal);
    expect(queued.document.status).toBe("queued");
    expect(processing.document.status).toBe("processing");
    expect(processing.events.some((event) => event.event === "progress")).toBe(true);
    expect(ready.document.status).toBe("ready");
    expect(isFinalDocumentStatus(ready.document.status)).toBe(true);
    expect(again.document.status).toBe("ready");
  });

  it("rejects an expired upload link", async () => {
    const sources = createMemorySources();
    const file = new File(["%PDF-1.7"], "lesson.pdf", { type: "application/pdf" });
    await expect(
      sources.uploads.upload(
        {
          url: "https://uploads.example.test/expired",
          method: "PUT",
          expires_at: "2020-01-01T00:00:00Z",
        },
        file,
        signal,
        () => undefined,
      ),
    ).rejects.toBeInstanceOf(UploadExpiredError);
  });

  it("returns not found for an unknown document", async () => {
    const sources = createMemorySources();
    await expect(sources.documents.getTimeline("missing", signal)).rejects.toMatchObject({
      status: 404,
    } satisfies Partial<ApiError>);
  });

  it("hard-deletes a document from list and timeline lookups", async () => {
    const sources = createMemorySources();
    await sources.documents.deleteDocument("doc-ready", signal);
    const listed = await sources.documents.listDocuments(null, signal);
    expect(listed.documents.some((document) => document.id === "doc-ready")).toBe(false);
    await expect(sources.documents.getTimeline("doc-ready", signal)).rejects.toMatchObject({
      status: 404,
    } satisfies Partial<ApiError>);
  });

  it("persists extraction_mode on register and submit override", async () => {
    const sources = createMemorySources();
    const registration = await sources.uploads.register(
      {
        file_name: "lesson.pdf",
        content_type: "application/pdf",
        size_bytes: 8,
        extraction_mode: "pdfium",
      },
      signal,
    );
    expect(registration.job.extraction_mode).toBe("pdfium");
    await sources.uploads.upload(
      registration.upload,
      new Blob(["%PDF"]),
      signal,
      () => undefined,
    );
    const submitted = await sources.uploads.submit(registration.job.id, signal, {
      extraction_mode: "gemini",
    });
    expect(submitted.extraction_mode).toBe("gemini");
  });

  it("returns content fixtures and enforces the review quality gate", async () => {
    const sources = createMemorySources();
    const ready = await sources.documents.getContent("doc-ready", {}, signal);
    expect(ready.content.blocks.some((block) => block.kind === "table")).toBe(true);

    await expect(
      sources.documents.getContent("doc-review", { includeReview: false }, signal),
    ).rejects.toMatchObject({ status: 409 } satisfies Partial<ApiError>);

    const review = await sources.documents.getContent(
      "doc-review",
      { includeReview: true },
      signal,
    );
    expect(review.content.review?.[0]?.reason).toMatch(/legibility/i);
  });
});
