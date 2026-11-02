import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { ApiError } from "../../api/client";
import { defaultAppSources, type AppSources } from "../../app/sources";
import type { DocumentList } from "../../api/contracts";
import { firstDocumentPage } from "../../fixtures/documents";
import { renderWithSources } from "../../test/render";
import { DocumentDetailPage } from "./DocumentDetailPage";
import { DocumentListPage } from "./DocumentListPage";

function listSource(
  listDocuments: AppSources["documents"]["listDocuments"],
  deleteDocument: AppSources["documents"]["deleteDocument"] = () => Promise.resolve(),
): AppSources {
  return {
    ...defaultAppSources,
    documents: {
      listDocuments,
      getTimeline: (id, signal) => defaultAppSources.documents.getTimeline(id, signal),
      getContent: (id, options, signal) =>
        defaultAppSources.documents.getContent(id, options, signal),
      deleteDocument,
    },
    timelinePollIntervalMs: 60_000,
    timelinePollMaxIntervalMs: 60_000,
  };
}

function renderDocuments(sources?: AppSources) {
  return renderWithSources(
    <Routes>
      <Route path="/documents" element={<DocumentListPage />} />
      <Route path="/documents/:documentId" element={<DocumentDetailPage />} />
    </Routes>,
    { path: "/documents", ...(sources ? { sources } : {}) },
  );
}

describe("Document list", () => {
  it("shows a loading state and then the documents", async () => {
    let resolveList: (value: DocumentList) => void = () => undefined;
    renderDocuments(
      listSource(
        () =>
          new Promise((resolve) => {
            resolveList = resolve;
          }),
      ),
    );
    expect(screen.getByRole("status")).toHaveTextContent("Loading documents…");
    resolveList(firstDocumentPage);
    expect(await screen.findByRole("link", { name: /annual-report.pdf/ })).toHaveTextContent(
      "Processing",
    );
    expect(screen.getByRole("link", { name: /biology.pdf/ })).toHaveTextContent("Ready");
    expect(screen.getByRole("link", { name: /field-scan.pdf/ })).toHaveTextContent("Blocked");
  });

  it("shows an empty state", async () => {
    renderDocuments(
      listSource(() => Promise.resolve({ documents: [], next_cursor: null })),
    );
    expect(await screen.findByRole("heading", { name: "No documents yet" })).toBeInTheDocument();
    const uploadLinks = screen.getAllByRole("link", { name: "Upload a PDF" });
    expect(uploadLinks.length).toBeGreaterThan(0);
    for (const link of uploadLinks) {
      expect(link).toHaveAttribute("href", "/documents/new");
    }
  });

  it("retries a failed load without showing credential text", async () => {
    const user = userEvent.setup();
    let fail = true;
    renderDocuments(
      listSource(() => {
        if (fail) return Promise.reject(new ApiError(401, "missing or invalid API key"));
        return Promise.resolve(firstDocumentPage);
      }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("You are not authorized to do that.");
    expect(screen.queryByText(/API key/)).not.toBeInTheDocument();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("link", { name: /annual-report.pdf/ })).toBeInTheDocument();
  });

  it("keeps each document once when the next page overlaps", async () => {
    const user = userEvent.setup();
    renderDocuments();
    expect(await screen.findByRole("link", { name: /biology.pdf/ })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Load more documents" }));
    expect(await screen.findByRole("link", { name: /handwritten-notes.pdf/ })).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: /biology.pdf/ })).toHaveLength(1);
    expect(screen.getByRole("link", { name: /waiting-room.pdf/ })).toHaveTextContent("Queued");
  });

  it("opens a document from the keyboard", async () => {
    const user = userEvent.setup();
    renderDocuments();
    const link = await screen.findByRole("link", { name: /annual-report.pdf/ });
    link.focus();
    await user.keyboard("{Enter}");
    expect(
      await screen.findByRole("heading", { name: "annual-report.pdf" }),
    ).toBeInTheDocument();
    expect(screen.getByText("4 of 12 pages")).toBeInTheDocument();
  });

  it("deletes a document from the list after confirm", async () => {
    const user = userEvent.setup();
    let deletedId: string | null = null;
    renderDocuments(
      listSource(
        () =>
          Promise.resolve({
            documents: firstDocumentPage.documents.filter(
              (document) => document.id !== deletedId,
            ),
            next_cursor: null,
          }),
        (id) => {
          deletedId = id;
          return Promise.resolve();
        },
      ),
    );
    const biologyLink = await screen.findByRole("link", { name: /biology.pdf/ });
    const biologyItem = biologyLink.closest("li");
    if (!(biologyItem instanceof HTMLElement)) {
      throw new Error("expected biology list item");
    }
    const biologyDelete = within(biologyItem).getByRole("button", {
      name: "Delete document",
    });
    await user.click(biologyDelete);
    await user.click(screen.getByRole("button", { name: "Delete permanently" }));
    expect(deletedId).toBe("doc-ready");
    expect(await screen.findByRole("link", { name: /annual-report.pdf/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /biology.pdf/ })).not.toBeInTheDocument();
  });
});
