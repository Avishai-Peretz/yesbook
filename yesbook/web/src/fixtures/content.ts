import {
  BLOCK_KINDS,
  contentResponseSchema,
  extractedContentSchema,
  type ContentBlock,
  type ContentResponse,
  type ExtractedContent,
  type Provenance,
  type PublishedContentStatus,
} from "../api/contracts";
import {
  partialDocument,
  readyDocument,
  reviewDocument,
} from "./documents";

function provenance(overrides: Partial<Provenance> & Pick<Provenance, "target_id" | "attempt_id">): Provenance {
  return {
    extractor: "docling",
    extractor_version: "2",
    ...overrides,
  };
}

function original(documentId: string) {
  return {
    url: `https://originals.example.test/${documentId}.pdf`,
    method: "GET",
    expires_at: "2026-11-02T18:00:00Z",
  };
}

function content(input: {
  versionId: string;
  jobId: string;
  status: PublishedContentStatus;
  coverage: number;
  pages: number;
  blocks: ContentBlock[];
  review?: ExtractedContent["review"];
  missing?: ExtractedContent["missing"];
  createdAt?: string;
}): ExtractedContent {
  return extractedContentSchema.parse({
    version_id: input.versionId,
    job_id: input.jobId,
    status: input.status,
    coverage: input.coverage,
    pipeline_version: "pipeline-v1",
    pages: input.pages,
    blocks: input.blocks,
    created_at: input.createdAt ?? "2026-11-02T12:08:00Z",
    ...(input.review ? { review: input.review } : {}),
    ...(input.missing ? { missing: input.missing } : {}),
  });
}

function response(document: ExtractedContent): ContentResponse {
  return contentResponseSchema.parse({
    content: document,
    original: original(document.job_id),
  });
}

const readyBlocks: ContentBlock[] = [
  {
    kind: "heading",
    text: "Photosynthesis overview",
    page: 1,
    direction: "ltr",
    provenance: provenance({ target_id: "target-ready-1", attempt_id: "attempt-ready-1" }),
  },
  {
    kind: "paragraph",
    text: "Plants convert light into chemical energy during photosynthesis.\n\nKey points:\n- Light reactions make ATP\n- Dark reactions fix carbon",
    page: 1,
    direction: "ltr",
    provenance: provenance({
      target_id: "target-ready-1",
      attempt_id: "attempt-ready-1",
      extractor: "pdfium",
      extractor_version: "1",
    }),
  },
  {
    kind: "heading",
    text: "סיכום התהליך",
    page: 2,
    direction: "rtl",
    provenance: provenance({ target_id: "target-ready-2", attempt_id: "attempt-ready-1" }),
  },
  {
    kind: "paragraph",
    text: "אור השמש הופך לפחמימות בתאים הירוקים של הצמח.",
    page: 2,
    direction: "rtl",
    provenance: provenance({ target_id: "target-ready-2", attempt_id: "attempt-ready-1" }),
  },
  {
    kind: "table",
    page: 3,
    rows: [
      ["Stage", "Input", "Output"],
      ["Light", "Photons", "ATP"],
      ["Dark", "CO₂", "Sugar"],
    ],
    provenance: provenance({ target_id: "target-ready-3", attempt_id: "attempt-ready-1" }),
  },
  {
    kind: "formula",
    text: "6CO₂ + 6H₂O → C₆H₁₂O₆ + 6O₂",
    page: 3,
    direction: "ltr",
    provenance: provenance({ target_id: "target-ready-3", attempt_id: "attempt-ready-1" }),
  },
  {
    kind: "figure",
    text: "Leaf cross-section showing chloroplasts",
    page: 4,
    direction: "ltr",
    bbox: { left: 72, top: 120, right: 480, bottom: 360 },
    // Content API signs persisted asset.object into both asset_url and asset.
    asset_url: "https://assets.example.test/ver-ready/leaf-cross-section.png",
    asset: {
      url: "https://assets.example.test/ver-ready/leaf-cross-section.png",
      content_type: "image/png",
    },
    provenance: provenance({ target_id: "target-ready-4", attempt_id: "attempt-ready-1" }),
  },
];

export const readyContent: ContentResponse = response(
  content({
    versionId: readyDocument.version_id ?? "ver-ready",
    jobId: readyDocument.id,
    status: "ready",
    coverage: 1,
    pages: 4,
    blocks: readyBlocks,
  }),
);

export const rtlContent: ContentResponse = response(
  content({
    versionId: "ver-rtl",
    jobId: "doc-rtl",
    status: "ready",
    coverage: 1,
    pages: 1,
    blocks: [
      {
        kind: "heading",
        text: "מבוא לביולוגיה",
        page: 1,
        direction: "rtl",
        provenance: provenance({ target_id: "target-rtl", attempt_id: "attempt-rtl-1" }),
      },
      {
        kind: "paragraph",
        text: "התא הוא יחידת החיים הבסיסית בכל אורגניזם.",
        page: 1,
        direction: "rtl",
        provenance: provenance({ target_id: "target-rtl", attempt_id: "attempt-rtl-1" }),
      },
      {
        kind: "table",
        page: 1,
        direction: "rtl",
        rows: [
          ["מונח", "הגדרה"],
          ["גרעין", "מכיל את החומר התורשתי"],
          ["מיטוכונדריה", "מייצרת אנרגיה לתא"],
        ],
        provenance: provenance({ target_id: "target-rtl", attempt_id: "attempt-rtl-1" }),
      },
    ],
  }),
);

export const partialContent: ContentResponse = response(
  content({
    versionId: partialDocument.version_id ?? "ver-partial",
    jobId: partialDocument.id,
    status: "partial",
    coverage: 0.5,
    pages: 4,
    blocks: [
      {
        kind: "heading",
        text: "Recovered pages",
        page: 1,
        direction: "ltr",
        provenance: provenance({ target_id: "target-partial-1", attempt_id: "attempt-partial-1" }),
      },
      {
        kind: "paragraph",
        text: "Only the undamaged pages could be extracted.",
        page: 1,
        direction: "ltr",
        provenance: provenance({ target_id: "target-partial-1", attempt_id: "attempt-partial-1" }),
      },
      {
        kind: "table",
        page: 2,
        rows: [
          ["Page", "Outcome"],
          ["1", "Extracted"],
          ["2", "Extracted"],
        ],
        provenance: provenance({ target_id: "target-partial-2", attempt_id: "attempt-partial-1" }),
      },
    ],
    missing: [
      {
        target_id: "target-partial-3",
        pages: [3],
        reason: "page image was unreadable",
      },
      {
        target_id: "target-partial-4",
        pages: [4],
        reason: "extractor timed out",
      },
    ],
    createdAt: "2026-11-02T09:11:00Z",
  }),
);

export const reviewContent: ContentResponse = response(
  content({
    versionId: reviewDocument.version_id ?? "ver-review",
    jobId: reviewDocument.id,
    status: "needs_review",
    coverage: 0.75,
    pages: 4,
    blocks: [
      {
        kind: "paragraph",
        text: "Most pages were accepted, but handwriting on page 3 needs a human check.",
        page: 1,
        direction: "ltr",
        provenance: provenance({ target_id: "target-review-1", attempt_id: "attempt-review-1" }),
      },
  {
    kind: "figure",
    text: "Handwritten diagram of a plant cell",
    page: 3,
    direction: "ltr",
    asset_url: "https://assets.example.test/ver-review/plant-cell.png",
    provenance: provenance({ target_id: "target-review-3", attempt_id: "attempt-review-1" }),
  },
      {
        kind: "formula",
        text: "E = mc²",
        page: 4,
        direction: "ltr",
        provenance: provenance({ target_id: "target-review-4", attempt_id: "attempt-review-1" }),
      },
    ],
    review: [
      {
        target_id: "target-review-3",
        pages: [3],
        reason: "low legibility on handwritten ink",
        code: "coverage_low",
        failed_checks: ["legibility"],
      },
    ],
    createdAt: "2026-11-02T10:12:00Z",
  }),
);

/** Automatic+images CV that stopped at review when only PDFium was configured. */
export const figuresReviewContent: ContentResponse = response(
  content({
    versionId: "ver-figures-review",
    jobId: "doc-figures-review",
    status: "needs_review",
    coverage: 0,
    pages: 1,
    blocks: [
      {
        kind: "paragraph",
        text: "Avi Example — software engineer resume text extracted without the portrait.",
        page: 1,
        direction: "ltr",
        provenance: provenance({
          target_id: "target-figures-review",
          extractor: "pdfium",
          attempt_id: "attempt-figures-review-1",
        }),
      },
    ],
    review: [
      {
        target_id: "target-figures-review",
        pages: [1],
        reason: "embedded images were not represented as figures",
        code: "figures_missing",
        failed_checks: ["figures"],
      },
    ],
    createdAt: "2026-11-02T16:12:00Z",
  }),
);

export const emptyContent: ContentResponse = response(
  content({
    versionId: "ver-empty",
    jobId: "doc-empty",
    status: "ready",
    coverage: 0,
    pages: 1,
    blocks: [],
    createdAt: "2026-11-02T08:00:00Z",
  }),
);

export const automaticImagesContent: ContentResponse = response(
  content({
    versionId: "ver-automatic-images",
    jobId: "doc-automatic-images",
    status: "ready",
    coverage: 1,
    pages: 1,
    blocks: [
      {
        kind: "heading",
        text: "Curriculum vitae",
        page: 1,
        direction: "ltr",
        provenance: provenance({
          target_id: "target-auto-img",
          attempt_id: "attempt-auto-img-3",
          extractor: "gemini",
          extractor_version: "1",
        }),
      },
      {
        kind: "figure",
        text: "Profile photograph",
        page: 1,
        direction: "ltr",
        asset: {
          url: "https://assets.example.test/ver-automatic-images/profile.jpg",
          content_type: "image/jpeg",
        },
        provenance: provenance({
          target_id: "target-auto-img",
          attempt_id: "attempt-auto-img-3",
          extractor: "gemini",
          extractor_version: "1",
        }),
      },
    ],
    createdAt: "2026-11-02T15:38:00Z",
  }),
);

export const captionOnlyFigureContent: ContentResponse = response(
  content({
    versionId: "ver-caption-only",
    jobId: "doc-caption-only",
    status: "ready",
    coverage: 1,
    pages: 1,
    blocks: [
      {
        kind: "figure",
        text: "Diagram described without a stored image asset",
        page: 1,
        direction: "ltr",
        provenance: provenance({
          target_id: "target-caption",
          attempt_id: "attempt-caption-1",
          extractor: "gemini",
          extractor_version: "1",
        }),
      },
    ],
  }),
);

export const contentsByDocumentId: Record<string, ContentResponse> = {
  [readyDocument.id]: readyContent,
  [partialDocument.id]: partialContent,
  [reviewDocument.id]: reviewContent,
  "doc-rtl": rtlContent,
  "doc-empty": emptyContent,
  "doc-automatic-images": automaticImagesContent,
  "doc-caption-only": captionOnlyFigureContent,
};

/** Every current block kind appears at least once across fixtures. */
export const allBlockKindsFixtureCoverage = BLOCK_KINDS;

export const malformedContentBody = {
  content: {
    version_id: "ver-bad",
    job_id: "doc-bad",
    status: "ready",
    coverage: 1,
    pipeline_version: "pipeline-v1",
    pages: 1,
    blocks: [{ kind: "paragraph", page: 1 }],
    created_at: "2026-11-02T12:00:00Z",
  },
  original: original("doc-bad"),
};
