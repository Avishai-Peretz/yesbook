import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "./client";
import { createLiveSources } from "./live";

describe("live sources", () => {
  it("delegates document and upload operations to the API client", async () => {
    const signal = new AbortController().signal;
    const listDocuments = vi.fn().mockResolvedValue({ documents: [], next_cursor: null });
    const getTimeline = vi.fn().mockResolvedValue({
      document: {
        id: "doc-1",
        status: "ready",
        file_name: "a.pdf",
        size_bytes: 1,
        created_at: "2026-11-02T13:00:00Z",
        updated_at: "2026-11-02T13:00:00Z",
      },
      events: [],
      attempts: [],
    });
    const register = vi.fn().mockResolvedValue({
      job: {
        id: "doc-1",
        status: "awaiting_upload",
        file_name: "a.pdf",
        size_bytes: 1,
        created_at: "2026-11-02T13:00:00Z",
        updated_at: "2026-11-02T13:00:00Z",
      },
      upload: {
        url: "http://127.0.0.1:8080/uploads/staging/a",
        method: "PUT",
        expires_at: "2099-01-01T00:00:00Z",
      },
    });
    const submit = vi.fn().mockResolvedValue({
      id: "doc-1",
      status: "queued",
      file_name: "a.pdf",
      size_bytes: 1,
      created_at: "2026-11-02T13:00:00Z",
      updated_at: "2026-11-02T13:00:00Z",
    });
    const upload = vi.fn().mockResolvedValue(undefined);
    const deleteDocument = vi.fn().mockResolvedValue(undefined);
    const client: ApiClient = {
      health: vi.fn(),
      getDocument: vi.fn(),
      listDocuments,
      getTimeline,
      getContent: vi.fn(),
      register,
      submit,
      deleteDocument,
      upload,
    };

    const sources = createLiveSources(client);
    await sources.documents.listDocuments(null, signal);
    await sources.documents.getTimeline("doc-1", signal);
    await sources.documents.deleteDocument("doc-1", signal);
    await sources.uploads.register(
      { file_name: "a.pdf", content_type: "application/pdf", size_bytes: 1 },
      signal,
    );
    const target = {
      url: "http://127.0.0.1:8080/uploads/staging/a",
      method: "PUT",
      expires_at: "2099-01-01T00:00:00Z",
    };
    await sources.uploads.upload(target, new Blob(["x"]), signal, () => undefined);
    await sources.uploads.submit("doc-1", signal);

    expect(listDocuments).toHaveBeenCalledWith(null, signal);
    expect(getTimeline).toHaveBeenCalledWith("doc-1", signal);
    expect(deleteDocument).toHaveBeenCalledWith("doc-1", signal);
    expect(register).toHaveBeenCalled();
    expect(upload).toHaveBeenCalledWith(target, expect.any(Blob), signal, expect.any(Function));
    expect(submit).toHaveBeenCalledWith("doc-1", signal, undefined);
  });
});
