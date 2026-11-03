import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError, InvalidApiResponseError } from "../../api/client";
import type { AppSources } from "../../app/sources";
import {
  emptyContent,
  figuresReviewContent,
  partialContent,
  readyContent,
  reviewContent,
  rtlContent,
} from "../../fixtures/content";
import {
  figuresReviewTimeline,
  readyTimeline,
  reviewTimeline,
} from "../../fixtures/documents";
import { renderWithSources } from "../../test/render";
import { collectPathDecisions } from "../documents/model";
import { ContentViewer } from "./ContentViewer";
import { ExtractedContentSection } from "./ExtractedContentSection";

function contentSources(
  getContent: AppSources["documents"]["getContent"],
): AppSources {
  return {
    documents: {
      listDocuments: () => Promise.resolve({ documents: [], next_cursor: null }),
      getTimeline: () => Promise.resolve(readyTimeline),
      getContent,
      deleteDocument: () => Promise.resolve(),
    },
    uploads: {
      register: () => new Promise(() => undefined),
      upload: () => new Promise(() => undefined),
      submit: () => new Promise(() => undefined),
    },
    timelinePollIntervalMs: 60_000,
    timelinePollMaxIntervalMs: 60_000,
  };
}

describe("ContentViewer", () => {
  it("renders every block kind with accessible table headers and provenance", () => {
    renderWithSources(
      <ContentViewer state={{ status: "ready", response: readyContent }} fileName="biology.pdf" />,
    );

    expect(screen.getByRole("heading", { level: 2, name: "Extracted content" })).toBeInTheDocument();
    expect(screen.getByText("100%")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Heading, Page 1/i })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Paragraph, Page 1/i })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Table, Page 3/i })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Formula, Page 3/i })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Figure, Page 4/i })).toBeInTheDocument();
    expect(screen.getByText("Photosynthesis overview")).toBeInTheDocument();
    expect(
      screen.getByText(/Plants convert light into chemical energy during photosynthesis/),
    ).toBeInTheDocument();
    expect(screen.getByText(/Key points:/)).toBeInTheDocument();
    expect(screen.getByText("Extractors").closest("div")).toHaveTextContent("PDFium");
    expect(screen.getByText("6CO₂ + 6H₂O → C₆H₁₂O₆ + 6O₂")).toBeInTheDocument();
    expect(screen.getByText("Leaf cross-section showing chloroplasts")).toBeInTheDocument();
    expect(
      screen.getByRole("img", { name: "Leaf cross-section showing chloroplasts" }),
    ).toHaveAttribute("src", "https://assets.example.test/ver-ready/leaf-cross-section.png");

    const table = screen.getByRole("table", { name: /table on page 3/i });
    expect(within(table).getByRole("columnheader", { name: "Stage" })).toBeInTheDocument();
    expect(within(table).getByRole("columnheader", { name: "Input" })).toBeInTheDocument();
    expect(within(table).getByRole("cell", { name: "ATP" })).toBeInTheDocument();

    const rtlParagraph = screen.getByText("אור השמש הופך לפחמימות בתאים הירוקים של הצמח.");
    expect(rtlParagraph).toHaveAttribute("dir", "rtl");

    expect(screen.getAllByText("Provenance").length).toBeGreaterThan(0);
  });

  it("renders an RTL fixture with document direction and table headers", () => {
    renderWithSources(
      <ContentViewer state={{ status: "ready", response: rtlContent }} fileName="rtl.pdf" />,
    );

    const heading = screen.getByText("מבוא לביולוגיה");
    expect(heading).toHaveAttribute("dir", "rtl");
    const table = screen.getByRole("table", { name: /table on page 1/i });
    expect(table).toHaveAttribute("dir", "rtl");
    expect(within(table).getByRole("columnheader", { name: "מונח" })).toBeInTheDocument();
    expect(within(table).getByRole("cell", { name: "מייצרת אנרגיה לתא" })).toBeInTheDocument();
  });

  it("shows partial coverage and missing-page warnings", () => {
    renderWithSources(
      <ContentViewer
        state={{ status: "ready", response: partialContent }}
        fileName="damaged-pages.pdf"
      />,
    );

    expect(screen.getByText("50%")).toBeInTheDocument();
    expect(
      screen.getByText("Extraction finished with partial coverage. Some pages are missing."),
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Missing pages" })).toBeInTheDocument();
    expect(screen.getByText(/page 3: page image was unreadable/i)).toBeInTheDocument();
  });

  it("shows review warnings for needs_review outcomes", () => {
    renderWithSources(
      <ContentViewer
        state={{ status: "ready", response: reviewContent }}
        fileName="handwritten-notes.pdf"
      />,
    );

    expect(screen.getByRole("heading", { name: "Needs review" })).toBeInTheDocument();
    expect(
      screen.getByText(/This document needs review before downstream use/i),
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Review warnings" })).toBeInTheDocument();
    expect(screen.getByText(/page 3: low legibility on handwritten ink/i)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Recommended next action" })).toBeInTheDocument();
  });

  it("shows actionable review guidance for figures_missing after chain exhaustion", () => {
    renderWithSources(
      <ContentViewer
        state={{ status: "ready", response: figuresReviewContent }}
        fileName="cv-photo-only-pdfium.pdf"
        pathDecisions={collectPathDecisions(figuresReviewTimeline)}
      />,
    );

    expect(screen.getByRole("heading", { name: "Needs review" })).toBeInTheDocument();
    expect(screen.getByText("0% accepted")).toBeInTheDocument();
    expect(screen.getAllByText("figures_missing").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Embedded images were not represented/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/Upload again after Docling is configured/i)).toBeInTheDocument();
  });

  it("falls back to caption when a figure has no asset URL", () => {
    const provenance = readyContent.content.blocks[0]?.provenance;
    expect(provenance).toBeDefined();
    renderWithSources(
      <ContentViewer
        state={{
          status: "ready",
          response: {
            content: {
              ...readyContent.content,
              blocks: [
                {
                  kind: "figure",
                  text: "Caption without bytes",
                  page: 1,
                  provenance: provenance ?? {
                    target_id: "target",
                    extractor: "gemini",
                    extractor_version: "1",
                    attempt_id: "attempt",
                  },
                },
              ],
            },
            original: readyContent.original,
          },
        }}
        fileName="caption.pdf"
      />,
    );

    expect(screen.getByText("Caption without bytes")).toBeInTheDocument();
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(screen.getByText(/Caption only/i)).toBeInTheDocument();
  });

  it("shows an empty state when no blocks were extracted", () => {
    renderWithSources(
      <ContentViewer
        state={{ status: "empty", content: emptyContent.content }}
        fileName="blank.pdf"
      />,
    );

    expect(screen.getByRole("status")).toHaveTextContent("No blocks extracted");
  });

  it("shows unavailable copy while processing", () => {
    renderWithSources(
      <ContentViewer
        state={{
          status: "unavailable",
          message: "Extracted content will appear when processing finishes.",
        }}
        fileName="annual-report.pdf"
      />,
    );

    expect(
      screen.getByText("Extracted content will appear when processing finishes."),
    ).toBeInTheDocument();
  });

  it("retries after a content load error", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    renderWithSources(
      <ContentViewer
        state={{
          status: "error",
          message: "The server returned data the app could not read.",
          onRetry,
        }}
        fileName="biology.pdf"
      />,
    );

    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});

describe("ExtractedContentSection", () => {
  it("loads fixture content for a ready document", async () => {
    renderWithSources(
      <ExtractedContentSection
        documentId="doc-ready"
        documentStatus="ready"
        fileName="biology.pdf"
      />,
      {
        sources: contentSources(() => Promise.resolve(readyContent)),
      },
    );

    expect(await screen.findByText("Photosynthesis overview")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Stage" })).toBeInTheDocument();
  });

  it("requests review content and surfaces review warnings", async () => {
    let includeReview = false;
    renderWithSources(
      <ExtractedContentSection
        documentId="doc-review"
        documentStatus="needs_review"
        fileName="handwritten-notes.pdf"
      />,
      {
        sources: contentSources((_documentId, options) => {
          includeReview = Boolean(options.includeReview);
          return Promise.resolve(reviewContent);
        }),
      },
    );

    expect(await screen.findByText(/low legibility on handwritten ink/i)).toBeInTheDocument();
    expect(includeReview).toBe(true);
    expect(reviewTimeline.document.status).toBe("needs_review");
  });

  it("shows a malformed-content failure with retry", async () => {
    const user = userEvent.setup();
    let fail = true;
    renderWithSources(
      <ExtractedContentSection
        documentId="doc-ready"
        documentStatus="ready"
        fileName="biology.pdf"
      />,
      {
        sources: contentSources(() => {
          if (fail) return Promise.reject(new InvalidApiResponseError("malformed"));
          return Promise.resolve(readyContent);
        }),
      },
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The server returned data the app could not read.",
    );
    fail = false;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("Photosynthesis overview")).toBeInTheDocument();
  });

  it("does not fetch content for unpublished documents", async () => {
    let called = false;
    renderWithSources(
      <ExtractedContentSection
        documentId="doc-processing"
        documentStatus="processing"
        fileName="annual-report.pdf"
      />,
      {
        sources: contentSources(() => {
          called = true;
          return Promise.reject(new ApiError(409, "not published"));
        }),
      },
    );

    expect(
      await screen.findByText("Extracted content will appear when processing finishes."),
    ).toBeInTheDocument();
    expect(called).toBe(false);
  });
});
