import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { ApiError, InvalidApiResponseError } from "../../api/client";
import type { AppSources } from "../../app/sources";
import type { DocumentTimeline } from "../../api/contracts";
import {
  automaticImagesTimeline,
  blockedTimeline,
  figuresReviewTimeline,
  hashMatchTimeline,
  processingTimeline,
} from "../../fixtures/documents";
import { renderWithSources } from "../../test/render";
import { DocumentDetailPage } from "./DocumentDetailPage";

function renderDetail(
  id: string,
  getTimeline: AppSources["documents"]["getTimeline"],
  intervalMs = 40,
  deleteDocument: AppSources["documents"]["deleteDocument"] = () => Promise.resolve(),
) {
  const sources: AppSources = {
    documents: {
      listDocuments: () => Promise.resolve({ documents: [], next_cursor: null }),
      getTimeline,
      getContent: () =>
        Promise.reject(new ApiError(409, "document has no published content")),
      deleteDocument,
    },
    uploads: {
      register: () => new Promise(() => undefined),
      upload: () => new Promise(() => undefined),
      submit: () => new Promise(() => undefined),
    },
    timelinePollIntervalMs: intervalMs,
    timelinePollMaxIntervalMs: intervalMs * 4,
  };
  return renderWithSources(
    <Routes>
      <Route path="/documents" element={<p>Documents home</p>} />
      <Route path="/documents/:documentId" element={<DocumentDetailPage />} />
    </Routes>,
    { path: `/documents/${id}`, sources },
  );
}

describe("Document detail", () => {
  it("shows the stage stepper, retry, progress, and attempt cost", async () => {
    renderDetail("doc-blocked", () => Promise.resolve(blockedTimeline));
    expect(await screen.findByRole("heading", { name: "field-scan.pdf" })).toBeInTheDocument();
    const current = screen.getByRole("listitem", { current: "step" });
    expect(current).toHaveTextContent("Extract");
    expect(current).toHaveTextContent("Failed");
    expect(current).toHaveTextContent("Retry 1");
    expect(current).toHaveTextContent("workflow-blocked · run-blocked-2");
    expect(screen.getByText("Route")).toBeInTheDocument();
    expect(screen.getAllByText("Blocked").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Pending").length).toBeGreaterThan(0);
    expect(screen.getAllByText("extractor timed out while reading page 4").length).toBeGreaterThan(0);
    expect(
      screen.getByText(/Retry with a different extractor or more context/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/Each stage writes evidence/i)).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Path" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Cost" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "$0.125" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "PDFium" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "Docling" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "Managed" })).toBeInTheDocument();
    expect(screen.getAllByRole("cell", { name: "docling 2" }).length).toBeGreaterThan(0);
  });

  it("shows the hash-match shortcut when cache reuse completed", async () => {
    renderDetail("doc-hash-match", () => Promise.resolve(hashMatchTimeline));
    expect(await screen.findByRole("heading", { name: "duplicate-report.pdf" })).toBeInTheDocument();
    expect(screen.getByText("Hash match: return the existing version.")).toBeInTheDocument();
    expect(screen.getAllByText("hash_match").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Ready").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Skipped").length).toBeGreaterThan(0);
  });

  it("shows automatic path decisions with stable codes", async () => {
    renderDetail("doc-automatic-images", () => Promise.resolve(automaticImagesTimeline));
    expect(await screen.findByRole("heading", { name: "cv-with-photo.pdf" })).toBeInTheDocument();
    expect(screen.getByText("Automatic")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Path decisions" })).toBeInTheDocument();
    expect(
      screen.getAllByText("embedded images were not represented as figures").length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("figures_missing").length).toBeGreaterThan(0);
    expect(
      screen.getAllByText(/docling url unset; continuing Automatic escalate without it/i).length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("extractor_unset").length).toBeGreaterThan(0);
    expect(screen.getAllByText("escalating to gemini").length).toBeGreaterThan(0);
  });

  it("shows chain_exhausted when Automatic+images has no further extractors", async () => {
    renderDetail("doc-figures-review", () => Promise.resolve(figuresReviewTimeline));
    expect(
      await screen.findByRole("heading", { name: "cv-photo-only-pdfium.pdf" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("figures_missing").length).toBeGreaterThan(0);
    expect(screen.getAllByText("extractor_unset").length).toBeGreaterThan(0);
    expect(screen.getAllByText("chain_exhausted").length).toBeGreaterThan(0);
    expect(
      screen.getAllByText(/no further extractors configured after pdfium/i).length,
    ).toBeGreaterThan(0);
  });

  it("deletes a document after confirm and stops polling", async () => {
    const user = userEvent.setup();
    let deleted = false;
    let calls = 0;
    renderDetail(
      "doc-processing",
      () => {
        calls += 1;
        return Promise.resolve(processingTimeline);
      },
      40,
      () => {
        deleted = true;
        return Promise.resolve();
      },
    );
    expect(await screen.findByText("4 of 12 pages")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete document" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Permanently delete");
    await user.click(screen.getByRole("button", { name: "Delete permanently" }));
    expect(await screen.findByText("Documents home")).toBeInTheDocument();
    expect(deleted).toBe(true);
    const settled = calls;
    await new Promise((resolve) => {
      setTimeout(resolve, 120);
    });
    expect(calls).toBe(settled);
  });

  it("shows bounded progress while extraction is running", async () => {
    renderDetail("doc-processing", () => Promise.resolve(processingTimeline), 60_000);
    expect(await screen.findByText("4 of 12 pages")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Extract progress" })).toHaveAttribute(
      "value",
      "4",
    );
  });

  it("retries a timeline error", async () => {
    const user = userEvent.setup();
    let fail = true;
    renderDetail("doc-processing", () => {
      if (fail) return Promise.reject(new InvalidApiResponseError("malformed"));
      return Promise.resolve(processingTimeline);
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The server returned data the app could not read.",
    );
    fail = false;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("4 of 12 pages")).toBeInTheDocument();
  });

  it("does not poll after a terminal failure", async () => {
    let calls = 0;
    renderDetail("doc-blocked", () => {
      calls += 1;
      return Promise.resolve(blockedTimeline);
    });
    expect((await screen.findAllByText("Retry 1")).length).toBeGreaterThan(0);
    await new Promise((resolve) => {
      setTimeout(resolve, 140);
    });
    expect(calls).toBe(1);
  });

  it("stops polling once a later snapshot is terminal", async () => {
    const frames = [processingTimeline, blockedTimeline];
    let calls = 0;
    renderDetail("doc-processing", () => {
      const frame = frames[Math.min(calls, frames.length - 1)];
      calls += 1;
      if (!frame) return Promise.reject(new ApiError(404, "not found"));
      return Promise.resolve(frame);
    });
    expect(await screen.findByText("4 of 12 pages")).toBeInTheDocument();
    expect((await screen.findAllByText("Retry 1")).length).toBeGreaterThan(0);
    const settled = calls;
    await new Promise((resolve) => {
      setTimeout(resolve, 160);
    });
    expect(calls).toBe(settled);
  });

  it("cancels an in-flight poll on unmount and does not overlap polls", async () => {
    let calls = 0;
    let pollSignal: AbortSignal | undefined;
    const { unmount } = renderDetail("doc-processing", (_id, signal) => {
      calls += 1;
      if (calls === 1) return Promise.resolve(processingTimeline);
      pollSignal = signal;
      return new Promise<DocumentTimeline>(() => undefined);
    });
    expect(await screen.findByText("4 of 12 pages")).toBeInTheDocument();
    await waitFor(() => {
      expect(calls).toBe(2);
    });
    await new Promise((resolve) => {
      setTimeout(resolve, 120);
    });
    expect(calls).toBe(2);
    unmount();
    await waitFor(() => {
      expect(pollSignal?.aborted).toBe(true);
    });
  });
});
